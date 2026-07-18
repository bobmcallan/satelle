// Package mirror is the push-fed read-only state for satelle serve
// (epic:serve-split / sty_dbdadfa0). Partitioned by repo_key — never a flat
// multi-repo bag (decision-local-db-placement). Serve renders only from this
// store; it never opens ~/.satelle/<repo-key>/satelle.db.
package mirror

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultDirName is the subdirectory under GlobalDir() for the serve mirror.
const DefaultDirName = "serve"

// DefaultDBName is the mirror SQLite file name.
const DefaultDBName = "mirror.db"

// Store is the push-fed multi-repo mirror. All rows carry repo_key.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the mirror database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DefaultPath returns ~/.satelle/serve/mirror.db (or SATELLE_HOME).
func DefaultPath(globalDir string) string {
	return filepath.Join(globalDir, DefaultDirName, DefaultDBName)
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS partitions (
  repo_key   TEXT PRIMARY KEY,
  slug       TEXT NOT NULL DEFAULT '',
  seq        INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS items (
  repo_key TEXT NOT NULL,
  kind     TEXT NOT NULL,
  id       TEXT NOT NULL,
  payload  TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (repo_key, kind, id)
);
CREATE INDEX IF NOT EXISTS idx_items_repo_kind ON items(repo_key, kind);
CREATE TABLE IF NOT EXISTS docs (
  repo_key TEXT NOT NULL,
  kind     TEXT NOT NULL,
  name     TEXT NOT NULL,
  payload  TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (repo_key, kind, name)
);
`)
	return err
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// TouchPartition ensures a partition row exists and bumps seq.
func (s *Store) TouchPartition(ctx context.Context, repoKey, slug string, now time.Time) (seq int64, err error) {
	repoKey = strings.TrimSpace(repoKey)
	if repoKey == "" {
		return 0, fmt.Errorf("mirror: empty repo_key")
	}
	at := now.UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO partitions (repo_key, slug, seq, updated_at) VALUES (?, ?, 1, ?)
ON CONFLICT(repo_key) DO UPDATE SET
  seq = seq + 1,
  slug = CASE WHEN excluded.slug != '' THEN excluded.slug ELSE partitions.slug END,
  updated_at = excluded.updated_at
`, repoKey, slug, at)
	if err != nil {
		return 0, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT seq FROM partitions WHERE repo_key = ?`, repoKey).Scan(&seq)
	return seq, err
}

// UpsertItem stores one work item JSON payload under (repo_key, kind, id).
func (s *Store) UpsertItem(ctx context.Context, repoKey, kind, id string, payload any, now time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO items (repo_key, kind, id, payload, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(repo_key, kind, id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at
`, repoKey, kind, id, string(b), at)
	return err
}

// ReplaceKind deletes all items of kind for repoKey then inserts the given set.
// Used by full snapshot reconcile.
func (s *Store) ReplaceKind(ctx context.Context, repoKey, kind string, items []ItemRow, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE repo_key = ? AND kind = ?`, repoKey, kind); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO items (repo_key, kind, id, payload, updated_at) VALUES (?, ?, ?, ?, ?)
`, repoKey, kind, it.ID, it.Payload, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ItemRow is a raw stored item.
type ItemRow struct {
	ID      string
	Payload string
}

// ListItems returns all items of kind for a partition.
func (s *Store) ListItems(ctx context.Context, repoKey, kind string) ([]ItemRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, payload FROM items WHERE repo_key = ? AND kind = ? ORDER BY id
`, repoKey, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ItemRow
	for rows.Next() {
		var r ItemRow
		if err := rows.Scan(&r.ID, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetItem returns one item payload or sql.ErrNoRows.
func (s *Store) GetItem(ctx context.Context, repoKey, kind, id string) (string, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `
SELECT payload FROM items WHERE repo_key = ? AND kind = ? AND id = ?
`, repoKey, kind, id).Scan(&payload)
	return payload, err
}

// ListPartitions returns known repo keys.
func (s *Store) ListPartitions(ctx context.Context) ([]Partition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repo_key, slug, seq, updated_at FROM partitions ORDER BY slug, repo_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Partition
	for rows.Next() {
		var p Partition
		if err := rows.Scan(&p.RepoKey, &p.Slug, &p.Seq, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Partition is one repo's mirror slice metadata.
type Partition struct {
	RepoKey   string
	Slug      string
	Seq       int64
	UpdatedAt string
}

// ApplyChange records a coarse change event (order:2 publisher). Bumps seq and
// leaves item bodies unchanged — full bodies arrive via snapshot (order:4).
func (s *Store) ApplyChange(ctx context.Context, repoKey, topic string, now time.Time) (seq int64, err error) {
	return s.TouchPartition(ctx, repoKey, "", now)
}
