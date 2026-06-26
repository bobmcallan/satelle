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
    status              TEXT NOT NULL DEFAULT 'open',
    priority            TEXT NOT NULL DEFAULT '',
    category            TEXT NOT NULL DEFAULT '',
    parent_id           TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    tags                TEXT NOT NULL DEFAULT '[]',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_work_items_kind   ON work_items(kind, status);
CREATE INDEX IF NOT EXISTS idx_work_items_parent ON work_items(parent_id);`

// Migrate creates the work_items table on db. Idempotent.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("workitem: migrate: %w", err)
	}
	return nil
}

// Store wraps work_items operations against a shared sqlite handle.
type Store struct{ db *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

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
		status = StatusOpen
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
	}
	tagsJSON, _ := json.Marshal(it.Tags)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO work_items
            (id, kind, title, body, status, priority, category, parent_id, acceptance_criteria, tags, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(it.Kind), it.Title, it.Body, it.Status, it.Priority, it.Category,
		it.ParentID, it.AcceptanceCriteria, string(tagsJSON),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Item{}, fmt.Errorf("workitem: create: %w", err)
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
// both); Status and ParentID further constrain. All fields are optional.
type ListFilter struct {
	Kind     Kind
	Status   string
	ParentID string
	Limit    int // <=0 ⇒ default 500, capped at 2000
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

// selectCols is the shared SELECT prefix for Get/List, fixing column order so
// one scan() serves both.
const selectCols = `SELECT id, kind, title, body, status, priority, category, parent_id, acceptance_criteria, tags, created_at, updated_at FROM work_items`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scan(sc scanner) (Item, error) {
	var (
		it               Item
		kind, tags       string
		created, updated string
	)
	if err := sc.Scan(&it.ID, &kind, &it.Title, &it.Body, &it.Status,
		&it.Priority, &it.Category, &it.ParentID, &it.AcceptanceCriteria,
		&tags, &created, &updated); err != nil {
		return Item{}, err
	}
	it.Kind = Kind(kind)
	_ = json.Unmarshal([]byte(tags), &it.Tags)
	it.Tags = nonNilTags(it.Tags)
	it.CreatedAt = parseTime(created)
	it.UpdatedAt = parseTime(updated)
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
