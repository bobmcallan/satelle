package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"
)

// schema is the evidence table, self-migrating (CREATE IF NOT EXISTS). No
// UPDATE/DELETE path exists, so the log is append-only by construction — the one
// exception is linkLegacyChains, which writes base_id once on rows that predate
// the column and touches nothing else. base_id and workflow_hash are added to an
// older table by Migrate (addColumns), so they are not part of any index here.
const schema = `
CREATE TABLE IF NOT EXISTS evidence (
    id            TEXT PRIMARY KEY,
    story_id      TEXT NOT NULL DEFAULT '',
    project_id    TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    actor         TEXT NOT NULL DEFAULT '',
    body          TEXT NOT NULL DEFAULT '',
    payload       TEXT NOT NULL DEFAULT '{}',
    refs          TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL,
    base_id       TEXT NOT NULL DEFAULT '',
    workflow_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_evidence_story ON evidence(story_id, created_at);
CREATE INDEX IF NOT EXISTS idx_evidence_kind  ON evidence(kind);`

// entryCols is the column list every read selects, in the order scanEntry reads.
const entryCols = `id, story_id, project_id, kind, actor, body, payload, refs, created_at, base_id, workflow_hash`

// metaSchema holds per-database facts that must not survive the database being
// replaced. The only key is instance_id, written once when the file is created.
const metaSchema = `
CREATE TABLE IF NOT EXISTS ledger_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`

const (
	instanceIDKey = "instance_id"
	// chainLinkedKey marks a database whose pre-existing rows have been linked
	// into per-story chains; linkLegacyChains runs once and never again.
	chainLinkedKey = "chain_linked"
)

// Migrate creates the evidence table on db. Idempotent. Called by the store
// opener alongside the other dynamic primitives' migrations.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("ledger: migrate: %w", err)
	}
	if err := addColumns(db); err != nil {
		return err
	}
	if _, err := db.Exec(metaSchema); err != nil {
		return fmt.Errorf("ledger: migrate: %w", err)
	}
	if err := linkLegacyChains(db); err != nil {
		return err
	}
	// Read first so a plain open of an existing database takes no write lock.
	var have string
	err := db.QueryRow(`SELECT value FROM ledger_meta WHERE key = ?`, instanceIDKey).Scan(&have)
	if err == sql.ErrNoRows {
		_, err = db.Exec(`INSERT OR IGNORE INTO ledger_meta (key, value) VALUES (?, ?)`,
			instanceIDKey, uuid.NewV4().String())
	}
	if err != nil {
		return fmt.Errorf("ledger: migrate instance id: %w", err)
	}
	return nil
}

// addColumns adds the chain columns to an evidence table that predates them,
// then the index that serves the tail lookup. Each step is skipped when already
// present, so a plain open of a current database takes no write lock.
func addColumns(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('evidence')`)
	if err != nil {
		return fmt.Errorf("ledger: migrate columns: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("ledger: migrate columns: %w", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("ledger: migrate columns: %w", err)
	}
	rows.Close()
	for _, col := range []string{"base_id", "workflow_hash"} {
		if have[col] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE evidence ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("ledger: migrate %s column: %w", col, err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_evidence_base ON evidence(story_id, base_id)`); err != nil {
		return fmt.Errorf("ledger: migrate base index: %w", err)
	}
	return nil
}

// linkLegacyChains links every story's rows that predate base_id into one chain,
// each row naming the previous row of its story in insertion (rowid) order, then
// records chainLinkedKey so it never runs again. It writes base_id only: no other
// column changes and no row is dropped. workflow_hash stays empty on those rows
// because the workflow they ran under is not known. Reads first, so a database
// already linked takes no write lock.
func linkLegacyChains(db *sql.DB) error {
	var done string
	err := db.QueryRow(`SELECT value FROM ledger_meta WHERE key = ?`, chainLinkedKey).Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("ledger: migrate chain marker: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("ledger: link chains: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Re-check inside the write transaction: another opener may have linked first.
	err = tx.QueryRow(`SELECT value FROM ledger_meta WHERE key = ?`, chainLinkedKey).Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("ledger: migrate chain marker: %w", err)
	}
	if _, err := tx.Exec(`
        UPDATE evidence SET base_id = COALESCE((
            SELECT prev FROM (
                SELECT id, LAG(id) OVER (PARTITION BY story_id ORDER BY rowid) AS prev
                FROM evidence WHERE story_id <> ''
            ) AS linked WHERE linked.id = evidence.id), '')
        WHERE story_id <> '' AND base_id = ''`); err != nil {
		return fmt.Errorf("ledger: link chains: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO ledger_meta (key, value) VALUES (?, '1')`, chainLinkedKey); err != nil {
		return fmt.Errorf("ledger: link chains: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ledger: link chains: %w", err)
	}
	return nil
}

// execer is the subset of *sql.DB / *sql.Tx that this store uses, so a
// shallow copy can bind to an open transaction without a second connection.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Conn is the handle an append runs on — the plain database or the open
// transaction the store is bound to. A WorkflowVersionFunc reads through it, so
// a resolver called inside a transaction does not wait on a second connection
// (the pool holds one) that the transaction itself is holding.
type Conn = execer

// WorkflowVersionFunc returns the content hash of the workflow governing storyID,
// or "" when none applies or it cannot be told. It must not fail an append.
type WorkflowVersionFunc func(ctx context.Context, c Conn, storyID string) string

// WorkflowSource is one workflow version: its content hash and the source that
// hashes to it — both route halves, or the legacy workflow body. It is the
// payload of a KindWorkflowVersion event (sty_d84be8bc).
type WorkflowSource struct {
	Hash   string `json:"hash"`
	Done   string `json:"done,omitempty"`
	Step   string `json:"step,omitempty"`
	Legacy string `json:"legacy,omitempty"`
}

// hasSource reports whether src carries any text a version event can archive.
func (w WorkflowSource) hasSource() bool { return w.Done != "" || w.Step != "" || w.Legacy != "" }

// WorkflowSourceFunc is WorkflowVersionFunc with the source that produced the
// hash, from the same resolution, so the hash an event carries and the text the
// chain archives cannot differ. Hash is "" when none applies. It must not fail
// an append.
type WorkflowSourceFunc func(ctx context.Context, c Conn, storyID string) WorkflowSource

// Store wraps evidence-table operations against a shared sqlite handle.
type Store struct {
	db execer
	// sourceOf stamps WorkflowHash on an entry the caller gave none and, when the
	// resolution carries its source, makes sure the story's chain holds a
	// KindWorkflowVersion event for that hash (nil = none).
	sourceOf WorkflowSourceFunc
	// failAppend, when set, makes the next Append return that error (tests).
	failAppend error
}

// SetWorkflowVersion installs the resolver Append and AppendOnce use to stamp the
// workflow version a story is judged under. It is injected, not imported: the
// resolver needs the work-item and doc stores, which this package must not depend
// on. A copy made by WithTx keeps it.
func (s *Store) SetWorkflowVersion(fn WorkflowVersionFunc) {
	if s == nil {
		return
	}
	if fn == nil {
		s.sourceOf = nil
		return
	}
	s.sourceOf = func(ctx context.Context, c Conn, storyID string) WorkflowSource {
		return WorkflowSource{Hash: fn(ctx, c, storyID)}
	}
}

// SetWorkflowSource installs a resolver that also names the source of the version
// it returns, so Append and AppendOnce record a KindWorkflowVersion event, once
// per hash per story, ahead of the first event that carries the hash. It replaces
// SetWorkflowVersion; a copy made by WithTx keeps it.
func (s *Store) SetWorkflowSource(fn WorkflowSourceFunc) {
	if s != nil {
		s.sourceOf = fn
	}
}

// workflowHash is the hash an insert for storyID carries: the caller's own when
// given, else the resolver's, else "". When the resolver names its source and the
// story's chain has no KindWorkflowVersion for the hash, it is appended first, so
// the event that records the hash is never ahead of the version it names.
func (s *Store) workflowHash(ctx context.Context, storyID, given string) string {
	if given != "" || storyID == "" || s.sourceOf == nil {
		return given
	}
	src := s.sourceOf(ctx, s.db, storyID)
	if src.Hash != "" && src.hasSource() {
		s.recordVersion(ctx, storyID, src)
	}
	return src.Hash
}

// versionInput is the append of src as storyID's workflow_version event.
func versionInput(storyID string, src WorkflowSource) AppendInput {
	payload, _ := json.Marshal(src)
	return AppendInput{
		StoryID: storyID, Kind: KindWorkflowVersion, Actor: "satelle",
		Body:    "workflow version " + src.Hash,
		Payload: payload, Refs: versionRefs(storyID, src.Hash), WorkflowHash: src.Hash,
	}
}

// versionRefs is the identity a KindWorkflowVersion event claims once: its story
// and hash. It rides Refs so a pulled copy and a local one are the same event.
func versionRefs(storyID, hash string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"workflow_version": storyID + ":" + hash})
	return b
}

// recordVersion appends the version event for src to storyID's chain unless one
// is already there. Best-effort: a failure here must not fail the append that
// asked, and the next append for the story tries again.
func (s *Store) recordVersion(ctx context.Context, storyID string, src WorkflowSource) {
	refs := versionRefs(storyID, src.Hash)
	var have int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM evidence WHERE kind = ? AND refs = ? LIMIT 1`, KindWorkflowVersion, string(refs)).Scan(&have); err == nil {
		return
	}
	_, _, _ = s.AppendOnce(ctx, versionInput(storyID, src), time.Time{})
}

// tailBase is the SQL expression naming storyID's current last entry — the base
// of the entry being inserted — and its arguments. It is evaluated inside the
// INSERT statement, so sqlite's single writer serialises it: two concurrent
// appends cannot both read the same tail. An entry with no story has no chain.
func tailBase(storyID string) (string, []any) {
	if storyID == "" {
		return "''", nil
	}
	return `COALESCE((SELECT id FROM evidence WHERE story_id = ? ORDER BY rowid DESC LIMIT 1), '')`, []any{storyID}
}

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// WithTx returns a shallow copy bound to tx. The original store is unchanged.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	if s == nil {
		return nil
	}
	out := *s
	out.db = tx
	return &out
}

// InjectAppendError makes the next Append return err. Test seam for proving
// a failed ledger write rolls back the paired story-row UPDATE.
func (s *Store) InjectAppendError(err error) {
	if s != nil {
		s.failAppend = err
	}
}

// Append inserts one row and returns it. Kind is required; now defaults to
// time.Now().UTC() when zero. Payload/Refs default to {}/[] when empty.
func (s *Store) Append(ctx context.Context, in AppendInput, now time.Time) (Entry, error) {
	if s != nil && s.failAppend != nil {
		err := s.failAppend
		s.failAppend = nil
		return Entry{}, err
	}
	if strings.TrimSpace(in.Kind) == "" {
		return Entry{}, fmt.Errorf("ledger: kind required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	refs := in.Refs
	if len(refs) == 0 {
		refs = json.RawMessage("[]")
	}
	e := Entry{
		ID:        NewID(),
		StoryID:   in.StoryID,
		ProjectID: in.ProjectID,
		Kind:      in.Kind,
		Actor:     in.Actor,
		Body:      in.Body,
		Payload:   payload,
		Refs:      refs,
		CreatedAt: now,
	}
	e.WorkflowHash = s.workflowHash(ctx, in.StoryID, in.WorkflowHash)
	base, baseArgs := tailBase(e.StoryID)
	args := append([]any{e.ID, e.StoryID, e.ProjectID, e.Kind, e.Actor, e.Body,
		string(payload), string(refs), now.Format(time.RFC3339Nano)}, baseArgs...)
	args = append(args, e.WorkflowHash)
	err := s.db.QueryRowContext(ctx, `
        INSERT INTO evidence (id, story_id, project_id, kind, actor, body, payload, refs, created_at, base_id, workflow_hash)
        SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, `+base+`, ?
        RETURNING base_id`, args...).Scan(&e.BaseID)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: append: %w", err)
	}
	return e, nil
}

// AppendOnce appends in ONLY when no row of the same kind already carries the
// same Refs, and reports whether it did. The existence check and the insert are
// ONE statement, so of any number of concurrent callers (separate processes
// included — sqlite serialises the writers) exactly one inserts. Refs is
// required: it is the identity being claimed once (sty_4b694872: one fix-lane
// claim licenses one edit, and a check-then-append would let two parallel edits
// both pass the check).
func (s *Store) AppendOnce(ctx context.Context, in AppendInput, now time.Time) (Entry, bool, error) {
	if strings.TrimSpace(in.Kind) == "" {
		return Entry{}, false, fmt.Errorf("ledger: kind required")
	}
	if len(in.Refs) == 0 {
		return Entry{}, false, fmt.Errorf("ledger: append-once requires refs")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	e := Entry{
		ID: NewID(), StoryID: in.StoryID, ProjectID: in.ProjectID, Kind: in.Kind,
		Actor: in.Actor, Body: in.Body, Payload: payload, Refs: in.Refs, CreatedAt: now,
	}
	e.WorkflowHash = s.workflowHash(ctx, in.StoryID, in.WorkflowHash)
	base, baseArgs := tailBase(e.StoryID)
	args := append([]any{e.ID, e.StoryID, e.ProjectID, e.Kind, e.Actor, e.Body,
		string(payload), string(in.Refs), now.Format(time.RFC3339Nano)}, baseArgs...)
	args = append(args, e.WorkflowHash, in.Kind, string(in.Refs))
	err := s.db.QueryRowContext(ctx, `
        INSERT INTO evidence (id, story_id, project_id, kind, actor, body, payload, refs, created_at, base_id, workflow_hash)
        SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, `+base+`, ?
        WHERE NOT EXISTS (SELECT 1 FROM evidence WHERE kind = ? AND refs = ?)
        RETURNING base_id`, args...).Scan(&e.BaseID)
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("ledger: append once: %w", err)
	}
	return e, true, nil
}

// Upsert inserts an evidence row with a known id (workstate rehydrate). Uses
// INSERT OR IGNORE so re-run is idempotent and existing rows are never
// rewritten (append-only in spirit). Kind and ID are required; missing
// timestamps default to now; payload/refs default to {}/[] as Append does.
func (s *Store) Upsert(ctx context.Context, e Entry, now time.Time) (Entry, error) {
	if strings.TrimSpace(e.ID) == "" {
		return Entry{}, fmt.Errorf("ledger: upsert needs an id")
	}
	if strings.TrimSpace(e.Kind) == "" {
		return Entry{}, fmt.Errorf("ledger: kind required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	if len(e.Refs) == 0 {
		e.Refs = json.RawMessage("[]")
	}
	// The base it arrived with is kept; one arriving without is linked to the
	// story's current tail, as an append would be.
	base, baseArgs := "?", []any{e.BaseID}
	if e.BaseID == "" {
		base, baseArgs = tailBase(e.StoryID)
	}
	args := append([]any{e.ID, e.StoryID, e.ProjectID, e.Kind, e.Actor, e.Body,
		string(e.Payload), string(e.Refs), e.CreatedAt.UTC().Format(time.RFC3339Nano)}, baseArgs...)
	args = append(args, e.WorkflowHash)
	var linked string
	err := s.db.QueryRowContext(ctx, `
        INSERT OR IGNORE INTO evidence (id, story_id, project_id, kind, actor, body, payload, refs, created_at, base_id, workflow_hash)
        SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, `+base+`, ?
        RETURNING base_id`, args...).Scan(&linked)
	switch {
	case err == sql.ErrNoRows: // the id was already present: nothing was written
	case err != nil:
		return Entry{}, fmt.Errorf("ledger: upsert: %w", err)
	default:
		e.BaseID = linked
	}
	return e, nil
}

// ListByStory returns every entry for a story, oldest-first, optionally
// filtered to one kind.
func (s *Store) ListByStory(ctx context.Context, storyID, kind string) ([]Entry, error) {
	if strings.TrimSpace(storyID) == "" {
		return nil, fmt.Errorf("ledger: story_id required")
	}
	return s.List(ctx, ListFilter{StoryID: storyID, Kind: kind, Limit: MaxListLimit})
}

// GetByID returns one entry by its primary key, and whether it exists. A
// primary-key read the ListFilter surface cannot express (it selects by
// story/project/kind only), added rather than making callers scan a kind's
// whole result set for one id (sty_183a0510). Same table, same columns as
// List — no schema change.
func (s *Store) GetByID(ctx context.Context, id string) (Entry, bool, error) {
	if strings.TrimSpace(id) == "" {
		return Entry{}, false, fmt.Errorf("ledger: id required")
	}
	e, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM evidence WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("ledger: get by id: %w", err)
	}
	return e, true, nil
}

// DefaultListLimit and MaxListLimit own the row limit of List and ListNewest: a
// limit <= 0 means the default and anything above the max is capped. A caller
// that shows the limit to a person or a gate reads it here, never a literal.
const (
	DefaultListLimit = 200
	MaxListLimit     = 2000
)

// EffectiveLimit resolves a requested row limit: DefaultListLimit for n <= 0,
// capped at MaxListLimit.
func EffectiveLimit(n int) int {
	if n <= 0 {
		return DefaultListLimit
	}
	return min(n, MaxListLimit)
}

// Window states what a newest-window read returned against what matched: Total
// rows matched the filter, Limit is the effective limit applied, Omitted is how
// many older rows fell outside it (zero when all fit), and Max is the most one
// read can return — so a caller can tell "raise the limit" from "narrow the
// filter".
type Window struct {
	Total   int `json:"total"`
	Limit   int `json:"limit"`
	Omitted int `json:"omitted"`
	Max     int `json:"max"`
}

// listQuery validates f and returns the SELECT prefix (columns, then extra
// select expressions) and WHERE clause with its arguments, shared by List and
// ListNewest.
func listQuery(f ListFilter, extraCols string) (string, []any, error) {
	if strings.TrimSpace(f.StoryID) == "" &&
		strings.TrimSpace(f.ProjectID) == "" &&
		strings.TrimSpace(f.Kind) == "" {
		return "", nil, fmt.Errorf("ledger: at least one filter field required")
	}
	var (
		conds []string
		args  []any
	)
	add := func(col, val string) {
		if strings.TrimSpace(val) != "" {
			conds = append(conds, col+" = ?")
			args = append(args, val)
		}
	}
	add("story_id", f.StoryID)
	add("project_id", f.ProjectID)
	add("kind", f.Kind)
	if strings.TrimSpace(f.AfterID) != "" {
		conds = append(conds, "(created_at, id) > (SELECT created_at, id FROM evidence WHERE id = ?)")
		args = append(args, f.AfterID)
	}
	q := `SELECT ` + entryCols + extraCols +
		` FROM evidence WHERE ` + strings.Join(conds, " AND ")
	return q, args, nil
}

// rowScanner is the Scan of a *sql.Row or *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// scanEntry reads one entryCols row. extra receives trailing select expressions.
func scanEntry(r rowScanner, extra ...any) (Entry, error) {
	var (
		e             Entry
		payload, refs string
		created       string
	)
	dest := append([]any{&e.ID, &e.StoryID, &e.ProjectID, &e.Kind,
		&e.Actor, &e.Body, &payload, &refs, &created, &e.BaseID, &e.WorkflowHash}, extra...)
	if err := r.Scan(dest...); err != nil {
		return Entry{}, err
	}
	e.Payload = json.RawMessage(payload)
	e.Refs = json.RawMessage(refs)
	e.CreatedAt = parseTime(created)
	return e, nil
}

// scanEntries reads rows of the listQuery columns into entries in row order.
// extra receives the trailing select expression, when the query has one.
func scanEntries(rows *sql.Rows, extra ...any) ([]Entry, error) {
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows, extra...)
		if err != nil {
			return nil, fmt.Errorf("ledger: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// List returns the OLDEST EffectiveLimit entries matching the filter,
// oldest-first. At least one of StoryID/ProjectID/Kind must be set; an
// unfiltered scan is refused. A reader that wants the latest state of a long
// ledger uses ListNewest.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Entry, error) {
	q, args, err := listQuery(f, "")
	if err != nil {
		return nil, err
	}
	q += fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT %d", EffectiveLimit(f.Limit))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger: list: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// ListNewest returns the NEWEST EffectiveLimit entries matching the filter,
// still ordered oldest-first (the newest entry is last), with the Window saying
// how many older rows were left out. One statement reads the rows and the
// matched total, so the two cannot disagree.
func (s *Store) ListNewest(ctx context.Context, f ListFilter) ([]Entry, Window, error) {
	q, args, err := listQuery(f, ", COUNT(*) OVER ()")
	if err != nil {
		return nil, Window{}, err
	}
	limit := EffectiveLimit(f.Limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d", limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, Window{}, fmt.Errorf("ledger: list newest: %w", err)
	}
	defer rows.Close()
	var total int
	entries, err := scanEntries(rows, &total)
	if err != nil {
		return nil, Window{}, err
	}
	slices.Reverse(entries)
	return entries, Window{Total: total, Limit: limit, Omitted: max(0, total-len(entries)), Max: MaxListLimit}, nil
}

// ForEachKindPageSize is the page size ForEachKind reads per round-trip.
// Exported as a test seam so a test can exercise multi-page pagination by
// lowering it, instead of seeding thousands of rows to exceed the
// production default.
var ForEachKindPageSize = 2000

// ForEachKind scans every entry of kind (optionally scoped to storyID),
// oldest-first, paging internally by (created_at, id) so a scan larger than
// one page is never silently truncated the way a single List call (capped at
// 2000 rows) would be (sty_363eaf55 rework: the --all skill roll-up read
// only the oldest 2000 agent_invocation rows and dropped everything newer).
// fn is called once per entry in order; an error from fn stops the scan and
// is returned unchanged.
func (s *Store) ForEachKind(ctx context.Context, storyID, kind string, fn func(Entry) error) error {
	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("ledger: kind required")
	}
	pageSize := ForEachKindPageSize
	if pageSize <= 0 {
		pageSize = 2000
	}
	var afterCreated, afterID string
	for {
		conds := []string{"kind = ?"}
		args := []any{kind}
		if strings.TrimSpace(storyID) != "" {
			conds = append(conds, "story_id = ?")
			args = append(args, storyID)
		}
		if afterCreated != "" {
			conds = append(conds, "(created_at > ? OR (created_at = ? AND id > ?))")
			args = append(args, afterCreated, afterCreated, afterID)
		}
		q := `SELECT ` + entryCols + ` FROM evidence WHERE ` +
			strings.Join(conds, " AND ") +
			fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT %d", pageSize)

		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("ledger: for each kind: %w", err)
		}
		page, err := scanEntries(rows)
		rows.Close()
		if err != nil {
			return err
		}

		for _, e := range page {
			if err := fn(e); err != nil {
				return err
			}
		}
		if len(page) < pageSize {
			return nil
		}
		last := page[len(page)-1]
		afterCreated = last.CreatedAt.UTC().Format(time.RFC3339Nano)
		afterID = last.ID
	}
}

// Count returns the total number of ledger entries. Cheaper than List+len and
// not subject to List's "at least one filter required" guard.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evidence`).Scan(&n); err != nil {
		return 0, fmt.Errorf("ledger: count: %w", err)
	}
	return n, nil
}

// ListChangedSince returns evidence rows whose created_at is at or after the
// second-granularity bound of since, store-wide (no story filter — story-less
// rows included), oldest-first, paged by limit/offset (sty_88e83180). Zero since
// omits the time bound. Deliberately bypasses List's "at least one filter"
// guard, as ListAll does. limit defaults to 1000, hard-capped at 5000.
//
// See workitem.Store.ListChangedSince for the RFC3339Nano TEXT bound rationale.
func (s *Store) ListChangedSince(ctx context.Context, since time.Time, limit, offset int) ([]Entry, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + entryCols + ` FROM evidence`
	var args []any
	if !since.IsZero() {
		bound := since.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05")
		q += " WHERE created_at >= ?"
		args = append(args, bound)
	}
	q += fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger: list changed since: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// SeqEntry is an evidence row with its position in insertion order.
type SeqEntry struct {
	Seq int64
	Entry
}

// ListInsertedAfter returns up to limit rows whose insertion position (the
// table's rowid) is greater than afterSeq, in insertion order, store-wide. The
// log is append-only and INSERT OR IGNORE, so within one database file rowid
// grows with every insert whatever the row's created_at says — a row stamped
// earlier than a previous push's high-water mark is still found
// (sty_4a31e1ed). A position is only meaningful for the database file it was
// read from; pair a saved one with InstanceID and IDAtSeq. limit defaults to
// 1000, hard-capped at 5000.
func (s *Store) ListInsertedAfter(ctx context.Context, afterSeq int64, limit int) ([]SeqEntry, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT `+entryCols+`, rowid
        FROM evidence WHERE rowid > ? ORDER BY rowid ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("ledger: list inserted after: %w", err)
	}
	defer rows.Close()
	out := []SeqEntry{}
	for rows.Next() {
		var seq int64
		e, err := scanEntry(rows, &seq)
		if err != nil {
			return nil, fmt.Errorf("ledger: scan: %w", err)
		}
		out = append(out, SeqEntry{Seq: seq, Entry: e})
	}
	return out, rows.Err()
}

// IDAtSeq returns the id of the row at insertion position seq, and whether
// such a row exists. A saved position is trusted only while the row there is
// still the row it was taken from (a VACUUM may renumber rowids).
func (s *Store) IDAtSeq(ctx context.Context, seq int64) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM evidence WHERE rowid = ?`, seq).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("ledger: id at seq: %w", err)
	}
	return id, true, nil
}

// InstanceID identifies this database file: written once when it is created,
// kept by a VACUUM INTO copy, absent from a database built fresh in its place.
func (s *Store) InstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM ledger_meta WHERE key = ?`, instanceIDKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("ledger: instance id: %w", err)
	}
	return id, nil
}

// ListAll returns up to limit evidence rows newest-first (no story/kind filter).
// Used by ui push / mirror snapshot export — List refuses unfiltered scans.
// Newest-first keeps recent progress lights alive when the ledger exceeds the
// export budget (sty_c5065d05): oldest-only LIMIT permanently starved new work.
// limit<=0 defaults to 10000; hard-capped at 50000.
func (s *Store) ListAll(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 10000
	}
	if limit > 50000 {
		limit = 50000
	}
	// Newest window under the budget, returned oldest-first for callers that
	// expect chronological order (mirror still re-sorts per story).
	q := fmt.Sprintf(`SELECT `+entryCols+` FROM (
		SELECT `+entryCols+` FROM evidence
		ORDER BY created_at DESC, id DESC LIMIT %d
	) ORDER BY created_at ASC, id ASC`, limit)
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("ledger: list all: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// parseTime decodes an RFC3339Nano timestamp, returning the zero time on a
// malformed value (a stored row is always well-formed; this is defensive).
func parseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
