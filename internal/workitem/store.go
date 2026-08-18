package workitem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a Get/Update misses.
var ErrNotFound = errors.New("workitem: not found")

// schema is the work_items table holding both stories and tasks, partitioned
// by the kind column. Self-migrating (CREATE IF NOT EXISTS).
const schema = `
CREATE TABLE IF NOT EXISTS work_items (
    id                  TEXT PRIMARY KEY,
    kind                TEXT NOT NULL,
    title               TEXT NOT NULL DEFAULT '',
    body                TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'backlog',
    priority            TEXT NOT NULL DEFAULT '',
    category            TEXT NOT NULL DEFAULT '',
    parent_id           TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    tags                TEXT NOT NULL DEFAULT '[]',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    archived            INTEGER NOT NULL DEFAULT 0,
    assignee            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_work_items_kind   ON work_items(kind, status);
CREATE INDEX IF NOT EXISTS idx_work_items_parent ON work_items(parent_id);`

// Migrate creates the work_items table on db and brings existing rows to the
// current conventions. Idempotent.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("workitem: migrate: %w", err)
	}
	// 'open' was the former initial status; the canonical start state is now
	// 'backlog' (see satelle-workflow-review). Rename any pre-existing 'open' rows
	// so the backlog count, status, and progress lights read correctly. Idempotent
	// — after the rename there are no 'open' rows left to touch.
	if _, err := db.Exec(`UPDATE work_items SET status = 'backlog' WHERE status = 'open'`); err != nil {
		return fmt.Errorf("workitem: migrate open→backlog: %w", err)
	}
	// archived is record DISPOSITION, orthogonal to workflow status (sty_cd209b8a):
	// add it to pre-existing tables (CREATE IF NOT EXISTS never alters an existing
	// schema). Tolerate the column already existing so migration stays idempotent.
	if _, err := db.Exec(`ALTER TABLE work_items ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("workitem: migrate archived column: %w", err)
	}
	// park_origin is authoritative resume-to-origin state (sty_f75286dc) — not
	// ledger-derived. Empty when not parked.
	if _, err := db.Exec(`ALTER TABLE work_items ADD COLUMN park_origin TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("workitem: migrate park_origin column: %w", err)
	}
	// assignee is the hosted principal id that may engage this item
	// (sty_8ccaa906). Empty = unassigned. Existing rows survive as empty.
	if _, err := db.Exec(`ALTER TABLE work_items ADD COLUMN assignee TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("workitem: migrate assignee column: %w", err)
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

// Store wraps work_items operations against a shared sqlite handle.
type Store struct{ db execer }

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

// CreateInput is the typed shape of a create. Kind and Title are required;
// Status defaults to open when blank.
type CreateInput struct {
	Kind               Kind
	Title              string
	Body               string
	Status             string
	Priority           string
	Category           string
	ParentID           string
	AcceptanceCriteria string
	Tags               []string
	Assignee           string
}

// Create inserts a new item and returns it with its assigned id and timestamps.
func (s *Store) Create(ctx context.Context, in CreateInput, now time.Time) (Item, error) {
	if !in.Kind.valid() {
		return Item{}, fmt.Errorf("workitem: invalid kind %q", in.Kind)
	}
	if strings.TrimSpace(in.Title) == "" {
		return Item{}, fmt.Errorf("workitem: title required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = StatusBacklog
	}
	it := Item{
		ID:                 in.Kind.newID(),
		Kind:               in.Kind,
		Title:              in.Title,
		Body:               in.Body,
		Status:             status,
		Priority:           in.Priority,
		Category:           in.Category,
		ParentID:           in.ParentID,
		AcceptanceCriteria: in.AcceptanceCriteria,
		Tags:               nonNilTags(in.Tags),
		CreatedAt:          now,
		UpdatedAt:          now,
		Assignee:           in.Assignee,
	}
	tagsJSON, _ := json.Marshal(it.Tags)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO work_items
            (id, kind, title, body, status, priority, category, parent_id, acceptance_criteria, tags, created_at, updated_at, assignee)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(it.Kind), it.Title, it.Body, it.Status, it.Priority, it.Category,
		it.ParentID, it.AcceptanceCriteria, string(tagsJSON),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), it.Assignee)
	if err != nil {
		return Item{}, fmt.Errorf("workitem: create: %w", err)
	}
	return it, nil
}

// Upsert inserts or replaces an item by its existing id — the import path for a
// portable markdown story (Parse → Upsert), which must preserve the id and
// timestamps rather than mint a new one the way Create does. Missing timestamps
// default to now so a hand-authored file without them still lands.
func (s *Store) Upsert(ctx context.Context, it Item, now time.Time) (Item, error) {
	if strings.TrimSpace(it.ID) == "" {
		return Item{}, fmt.Errorf("workitem: upsert needs an id")
	}
	if !it.Kind.valid() {
		return Item{}, fmt.Errorf("workitem: invalid kind %q", it.Kind)
	}
	if strings.TrimSpace(it.Title) == "" {
		return Item{}, fmt.Errorf("workitem: title required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if it.CreatedAt.IsZero() {
		it.CreatedAt = now
	}
	if it.UpdatedAt.IsZero() {
		it.UpdatedAt = now
	}
	if strings.TrimSpace(it.Status) == "" {
		it.Status = StatusBacklog
	}
	it.Tags = nonNilTags(it.Tags)
	tagsJSON, _ := json.Marshal(it.Tags)
	archived := 0
	if it.Archived {
		archived = 1
	}
	// Markdown import does not carry assignee (machine-set). Keep the existing
	// holder when the incoming row leaves it blank so Upsert cannot wipe it.
	if strings.TrimSpace(it.Assignee) == "" {
		if existing, gerr := s.Get(ctx, it.ID); gerr == nil {
			it.Assignee = existing.Assignee
		}
	}
	// archived, park_origin and assignee must be listed: INSERT OR REPLACE
	// deletes+reinserts, so omitting a column would reset it to DEFAULT.
	_, err := s.db.ExecContext(ctx, `
        INSERT OR REPLACE INTO work_items
            (id, kind, title, body, status, priority, category, parent_id, acceptance_criteria, tags, created_at, updated_at, archived, park_origin, assignee)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(it.Kind), it.Title, it.Body, it.Status, it.Priority, it.Category,
		it.ParentID, it.AcceptanceCriteria, string(tagsJSON),
		it.CreatedAt.UTC().Format(time.RFC3339Nano), it.UpdatedAt.UTC().Format(time.RFC3339Nano), archived, it.ParkOrigin, it.Assignee)
	if err != nil {
		return Item{}, fmt.Errorf("workitem: upsert: %w", err)
	}
	return it, nil
}

// Get returns one item by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Item, error) {
	row := s.db.QueryRowContext(ctx, selectCols+` WHERE id = ?`, id)
	it, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("workitem: get: %w", err)
	}
	return it, nil
}

// ListFilter parameterises List. Kind narrows to stories or tasks (empty =
// both); Status and ParentID further constrain. Tag matches items that hold
// that exact tag among their multi-value set (ANY-match: a story with
// epic:this AND epic:that matches Tag "epic:this"). All fields are optional.
type ListFilter struct {
	Kind     Kind
	Status   string
	ParentID string
	Tag      string // exact tag match against the JSON tags array (sty_f7115cd2)
	Limit    int    // <=0 ⇒ default 500, capped at 2000
	// IncludeArchived returns archived (disposed) items too. The default (false)
	// excludes them — archive is a terminal disposition kept out of the working
	// list (sty_cd209b8a).
	IncludeArchived bool
}

// List returns items matching the filter, newest-updated first.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Item, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
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
	add("kind", string(f.Kind))
	add("status", f.Status)
	add("parent_id", f.ParentID)
	if !f.IncludeArchived {
		conds = append(conds, "archived = 0")
	}
	// Tag filter (sty_f7115cd2): tags are a JSON array of strings. json_each
	// expands elements so an exact value match implements ANY-match within a
	// multi-value namespace (repeated-key model). Applied in SQL so Limit is
	// correct after filtering.
	if tag := strings.TrimSpace(f.Tag); tag != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)`)
		args = append(args, tag)
	}

	q := selectCols
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY updated_at DESC, id ASC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("workitem: list: %w", err)
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("workitem: scan: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateInput carries the mutable fields. A nil pointer leaves a field
// unchanged; a non-nil pointer sets it (including to the empty value).
type UpdateInput struct {
	Title              *string
	Body               *string
	Status             *string
	Priority           *string
	Category           *string
	ParentID           *string
	AcceptanceCriteria *string
	Tags               *[]string
	ParkOrigin         *string
	Assignee           *string
}

// Update applies the set fields to the item and returns the updated row.
// updated_at is always advanced. Returns ErrNotFound for an unknown id.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput, now time.Time) (Item, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	var (
		sets = []string{"updated_at = ?"}
		args = []any{now.Format(time.RFC3339Nano)}
	)
	addStr := func(col string, v *string) {
		if v != nil {
			sets = append(sets, col+" = ?")
			args = append(args, *v)
		}
	}
	addStr("title", in.Title)
	addStr("body", in.Body)
	addStr("status", in.Status)
	addStr("priority", in.Priority)
	addStr("category", in.Category)
	addStr("parent_id", in.ParentID)
	addStr("acceptance_criteria", in.AcceptanceCriteria)
	addStr("park_origin", in.ParkOrigin)
	addStr("assignee", in.Assignee)
	if in.Tags != nil {
		tagsJSON, _ := json.Marshal(nonNilTags(*in.Tags))
		sets = append(sets, "tags = ?")
		args = append(args, string(tagsJSON))
	}
	args = append(args, id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE work_items SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return Item{}, fmt.Errorf("workitem: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Item{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// SetStatus is the focused status-change helper, advancing updated_at.
func (s *Store) SetStatus(ctx context.Context, id, status string, now time.Time) (Item, error) {
	return s.Update(ctx, id, UpdateInput{Status: &status}, now)
}

// Archive marks an item archived — a terminal bookkeeping DISPOSITION, orthogonal
// to workflow status (sty_cd209b8a). It uses a targeted UPDATE (not the
// INSERT-OR-REPLACE upsert path) so no other column is touched, and Get still
// returns the row with its full lineage. Idempotent; ErrNotFound if the id is
// unknown.
func (s *Store) Archive(ctx context.Context, id string, now time.Time) (Item, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE work_items SET archived = 1, updated_at = ? WHERE id = ?`,
		now.Format(time.RFC3339Nano), id)
	if err != nil {
		return Item{}, fmt.Errorf("workitem: archive: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Item{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// Count returns the number of items of a kind (empty kind = all kinds). Cheaper
// than List+len since it loads no rows.
func (s *Store) Count(ctx context.Context, kind Kind) (int, error) {
	q := `SELECT COUNT(*) FROM work_items`
	var args []any
	if strings.TrimSpace(string(kind)) != "" {
		q += ` WHERE kind = ?`
		args = append(args, string(kind))
	}
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("workitem: count: %w", err)
	}
	return n, nil
}

// Fingerprint returns a cheap change-signal for a kind — count plus the latest
// updated_at. It changes on any insert, update, or status change, so a poller
// can detect cross-process mutations (CLI edits) without loading rows.
func (s *Store) Fingerprint(ctx context.Context, kind Kind) (string, error) {
	q := `SELECT COUNT(*), COALESCE(MAX(updated_at), '') FROM work_items`
	var args []any
	if strings.TrimSpace(string(kind)) != "" {
		q += ` WHERE kind = ?`
		args = append(args, string(kind))
	}
	var (
		n   int
		max string
	)
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n, &max); err != nil {
		return "", fmt.Errorf("workitem: fingerprint: %w", err)
	}
	return fmt.Sprintf("%d:%s", n, max), nil
}

// ListChangedSince returns work items of the given kind whose updated_at is at
// or after the second-granularity bound of since, oldest-first, paged by
// limit/offset (sty_88e83180). Archived rows are included. Zero since omits the
// time bound (full set). limit defaults to 1000 and is hard-capped at 5000.
//
// The SQL bound uses whole-second RFC3339 without Z because updated_at is stored
// as TEXT via time.RFC3339Nano (trailing zeros trimmed) — a full nano cursor
// would not compare lexicographically. Rows inside the cursor's own second are
// re-sent (harmless upsert); skipping would lose records.
func (s *Store) ListChangedSince(ctx context.Context, kind Kind, since time.Time, limit, offset int) ([]Item, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	if offset < 0 {
		offset = 0
	}
	var (
		conds []string
		args  []any
	)
	if k := strings.TrimSpace(string(kind)); k != "" {
		conds = append(conds, "kind = ?")
		args = append(args, k)
	}
	if !since.IsZero() {
		// Truncate to whole seconds; format without Z for fixed-width prefix.
		bound := since.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05")
		conds = append(conds, "updated_at >= ?")
		args = append(args, bound)
	}
	q := selectCols
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY updated_at ASC, id ASC LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("workitem: list changed since: %w", err)
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("workitem: scan: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// selectCols is the shared SELECT prefix for Get/List, fixing column order so
// one scan() serves both.
const selectCols = `SELECT id, kind, title, body, status, priority, category, parent_id, acceptance_criteria, tags, created_at, updated_at, archived, park_origin, assignee FROM work_items`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scan(sc scanner) (Item, error) {
	var (
		it               Item
		kind, tags       string
		created, updated string
		archived         int
	)
	if err := sc.Scan(&it.ID, &kind, &it.Title, &it.Body, &it.Status,
		&it.Priority, &it.Category, &it.ParentID, &it.AcceptanceCriteria,
		&tags, &created, &updated, &archived, &it.ParkOrigin, &it.Assignee); err != nil {
		return Item{}, err
	}
	it.Kind = Kind(kind)
	_ = json.Unmarshal([]byte(tags), &it.Tags)
	it.Tags = nonNilTags(it.Tags)
	it.CreatedAt = parseTime(created)
	it.UpdatedAt = parseTime(updated)
	it.Archived = archived != 0
	return it, nil
}

func nonNilTags(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

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
