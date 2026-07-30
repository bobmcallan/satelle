// Package ledger persists cost rows. Schema changes go through migrations.
package ledger

import (
	"fmt"
	"strings"
	"time"
)

// Row is one persisted cost record.
type Row struct {
	ID         string
	Step       string
	DurationMS int64
	TokensIn   int
	TokensOut  int
	CreatedAt  time.Time
}

// Store is the fixture's persistence seam. The real implementation is SQLite;
// this fixture keeps the same surface over memory.
type Store struct {
	path string
	rows []Row
}

// Open applies every pending migration and returns a ready store.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Append persists one row, rejecting secret-shaped identifiers.
func (s *Store) Append(r Row) error {
	if err := rejectSecret(r.ID); err != nil {
		return err
	}
	s.rows = append(s.rows, r)
	return nil
}

// Rows returns every persisted row in insertion order.
func (s *Store) Rows() ([]Row, error) { return s.rows, nil }

func (s *Store) migrate() error {
	for i, stmt := range Migrations {
		if strings.TrimSpace(stmt) == "" {
			return fmt.Errorf("migration %d is empty", i+1)
		}
	}
	return nil
}

// Migrations is the ordered, append-only migration list. A new column is added
// by appending a statement here — never by editing an existing one, so an
// existing database upgrades in place.
var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS cost (
		id TEXT PRIMARY KEY,
		step TEXT NOT NULL,
		duration_ms INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`ALTER TABLE cost ADD COLUMN tokens_in INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE cost ADD COLUMN tokens_out INTEGER NOT NULL DEFAULT 0`,
}

func rejectSecret(id string) error {
	lower := strings.ToLower(id)
	for _, shape := range []string{"api_key", "bearer ", "password"} {
		if strings.Contains(lower, shape) {
			return fmt.Errorf("refusing secret-shaped id %q", id)
		}
	}
	return nil
}
