package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// schema is the evidence table, self-migrating (CREATE IF NOT EXISTS). No
// UPDATE/DELETE path exists, so the log is append-only by construction.
const schema = `
CREATE TABLE IF NOT EXISTS evidence (
    id         TEXT PRIMARY KEY,
    story_id   TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '{}',
    refs       TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_evidence_story ON evidence(story_id, created_at);
CREATE INDEX IF NOT EXISTS idx_evidence_kind  ON evidence(kind);`

// Migrate creates the evidence table on db. Idempotent. Called by the store
// opener alongside the other dynamic primitives' migrations.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("ledger: migrate: %w", err)
	}
	return nil
}

// Store wraps evidence-table operations against a shared sqlite handle.
type Store struct{ db *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Append inserts one row and returns it. Kind is required; now defaults to
// time.Now().UTC() when zero. Payload/Refs default to {}/[] when empty.
func (s *Store) Append(ctx context.Context, in AppendInput, now time.Time) (Entry, error) {
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
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO evidence (id, story_id, project_id, kind, actor, body, payload, refs, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.StoryID, e.ProjectID, e.Kind, e.Actor, e.Body,
		string(payload), string(refs), now.Format(time.RFC3339Nano))
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: append: %w", err)
	}
	return e, nil
}

// ListByStory returns every entry for a story, oldest-first, optionally
// filtered to one kind.
func (s *Store) ListByStory(ctx context.Context, storyID, kind string) ([]Entry, error) {
	if strings.TrimSpace(storyID) == "" {
		return nil, fmt.Errorf("ledger: story_id required")
	}
	return s.List(ctx, ListFilter{StoryID: storyID, Kind: kind, Limit: 2000})
}

// List returns entries matching the filter, oldest-first. At least one of
// StoryID/ProjectID/Kind must be set; an unfiltered scan is refused.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Entry, error) {
	if strings.TrimSpace(f.StoryID) == "" &&
		strings.TrimSpace(f.ProjectID) == "" &&
		strings.TrimSpace(f.Kind) == "" {
		return nil, fmt.Errorf("ledger: at least one filter field required")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
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
	add("story_id", f.StoryID)
	add("project_id", f.ProjectID)
	add("kind", f.Kind)

	q := `SELECT id, story_id, project_id, kind, actor, body, payload, refs, created_at FROM evidence`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger: list: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var (
			e             Entry
			payload, refs string
			created       string
		)
		if err := rows.Scan(&e.ID, &e.StoryID, &e.ProjectID, &e.Kind,
			&e.Actor, &e.Body, &payload, &refs, &created); err != nil {
			return nil, fmt.Errorf("ledger: scan: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.Refs = json.RawMessage(refs)
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
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
