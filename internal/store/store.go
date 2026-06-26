// Package store opens the per-repo sqlite database (.satelle/satelle.db) and
// wires the dynamic-primitive stores plus the authored-doc index onto the one
// shared handle.
//
// It follows satellites' internal/workstate opener verbatim — pure-Go
// modernc.org/sqlite (no cgo), WAL, busy_timeout, _txlock=immediate, and
// SetMaxOpenConns(1) so all writes funnel through a single writer (sqlite
// permits exactly one). Each domain package owns its own schema and self-
// migrates; Open orchestrates them so a fresh repo needs no setup step.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DB is the opened per-repo database with its domain stores attached. Callers
// reach data through the sub-stores; the raw handle stays encapsulated.
type DB struct {
	db       *sql.DB
	Ledger   *ledger.Store
	Stories  *workitem.Store // stories and tasks share one store; filter by Kind
	DocIndex *docindex.Store
}

// Open opens (creating if absent) the sqlite database at path, migrates every
// dynamic-primitive and index schema, and returns the wired DB. Parent dirs
// are created as needed, so Open(".satelle/satelle.db") works on a fresh repo.
func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("store: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
		}
	}
	// WAL + busy_timeout for concurrent readers and a contended single writer;
	// _txlock=immediate so a read-then-write txn takes its write lock at BEGIN
	// (where busy_timeout applies) instead of erroring on lock upgrade.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// sqlite permits exactly one writer; cap the pool to one connection so
	// concurrent writers serialise (wait) under busy_timeout rather than racing
	// the lock and surfacing SQLITE_BUSY. The per-repo file is low-volume, so a
	// single connection is not a throughput concern.
	sqldb.SetMaxOpenConns(1)

	for _, migrate := range []func(*sql.DB) error{
		ledger.Migrate,
		workitem.Migrate,
		docindex.Migrate,
	} {
		if err := migrate(sqldb); err != nil {
			sqldb.Close()
			return nil, err
		}
	}

	return &DB{
		db:       sqldb,
		Ledger:   ledger.New(sqldb),
		Stories:  workitem.New(sqldb),
		DocIndex: docindex.New(sqldb),
	}, nil
}

// SQL exposes the raw handle for callers that need it (tests, future stores).
func (d *DB) SQL() *sql.DB { return d.db }

// Close releases the underlying database handle.
func (d *DB) Close() error { return d.db.Close() }
