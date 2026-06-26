// Package docindex is satelle's directory monitor for authored markdown.
//
// The architecture splits the system of record: stories/tasks/ledger are
// dynamic sqlite primitives, while authored artifacts (documents, workflows,
// principles, skills) are MARKDOWN ON DISK — the files are the source of truth.
// This package syncs those files into a sqlite index so the CLI and web can
// query them without the markdown becoming a hand-managed store.
//
// Sync is the core: walk the configured per-kind dirs, upsert changed files
// (detected by size+mtime), and prune rows whose file disappeared. Watch wraps
// Sync in a poll loop — a dependency-free monitor (satellites indexes by
// scanning, not fsnotify), so the static no-cgo binary stays dependency-light.
// SQL is libSQL-compatible.
package docindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Doc is one indexed authored file.
type Doc struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name"` // filename without the .md extension
	Path      string    `json:"path"` // absolute path on disk
	Headline  string    `json:"headline,omitempty"`
	Body      string    `json:"body"`
	Hash      string    `json:"hash"` // sha256 of body, hex
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
	IndexedAt time.Time `json:"indexed_at"`
}

// schema is the authored-docs index, keyed by (kind, path). Self-migrating.
const schema = `
CREATE TABLE IF NOT EXISTS authored_docs (
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    path       TEXT NOT NULL,
    headline   TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    hash       TEXT NOT NULL DEFAULT '',
    size       INTEGER NOT NULL DEFAULT 0,
    mod_time   TEXT NOT NULL,
    indexed_at TEXT NOT NULL,
    PRIMARY KEY (kind, path)
);
CREATE INDEX IF NOT EXISTS idx_authored_docs_kind ON authored_docs(kind, name);`

// Migrate creates the authored_docs table on db. Idempotent.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("docindex: migrate: %w", err)
	}
	return nil
}

// Store indexes authored markdown into the authored_docs table.
type Store struct{ db *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// SyncResult reports what a Sync pass changed.
type SyncResult struct {
	Indexed int `json:"indexed"` // files inserted or updated
	Pruned  int `json:"pruned"`  // index rows whose file no longer exists
	Scanned int `json:"scanned"` // .md files seen on disk
}

// Sync brings the index in line with the markdown on disk for the given
// kind→dir map. For each kind it walks the dir (recursively), upserts every
// .md file whose size+mtime differs from the index, and prunes rows for files
// that disappeared. A missing dir is not an error — its rows are pruned (the
// kind simply has no authored content yet).
func (s *Store) Sync(ctx context.Context, dirs map[string]string, now time.Time) (SyncResult, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	var res SyncResult
	for _, kind := range sortedKeys(dirs) {
		dir := dirs[kind]
		onDisk, err := walkMarkdown(dir)
		if err != nil {
			return res, fmt.Errorf("docindex: scan %s: %w", dir, err)
		}
		res.Scanned += len(onDisk)

		indexed, err := s.indexedPaths(ctx, kind)
		if err != nil {
			return res, err
		}
		seen := make(map[string]struct{}, len(onDisk))
		for _, fileInfo := range onDisk {
			seen[fileInfo.path] = struct{}{}
			prev, ok := indexed[fileInfo.path]
			if ok && prev.size == fileInfo.size && prev.mod.Equal(fileInfo.mod) {
				continue // unchanged — skip the read+write
			}
			if err := s.upsert(ctx, kind, fileInfo, now); err != nil {
				return res, err
			}
			res.Indexed++
		}
		for path := range indexed {
			if _, ok := seen[path]; !ok {
				if err := s.delete(ctx, kind, path); err != nil {
					return res, err
				}
				res.Pruned++
			}
		}
	}
	return res, nil
}

// Watch runs Sync immediately, then on every interval tick until ctx is
// cancelled. onSync, if non-nil, is called with each pass's result (and any
// error) so callers can log progress. It returns ctx.Err() when cancelled.
// This is the "directory monitor": a poll loop, dependency-free.
func (s *Store) Watch(ctx context.Context, dirs map[string]string, interval time.Duration, onSync func(SyncResult, error)) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	run := func() {
		res, err := s.Sync(ctx, dirs, time.Now())
		if onSync != nil {
			onSync(res, err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			run()
		}
	}
}

// List returns the indexed docs for a kind, name-sorted. Empty kind returns
// every indexed doc across all kinds.
func (s *Store) List(ctx context.Context, kind string) ([]Doc, error) {
	q := `SELECT kind, name, path, headline, body, hash, size, mod_time, indexed_at FROM authored_docs`
	var args []any
	if strings.TrimSpace(kind) != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY kind ASC, name ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("docindex: list: %w", err)
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var (
			d              Doc
			modS, indexedS string
		)
		if err := rows.Scan(&d.Kind, &d.Name, &d.Path, &d.Headline, &d.Body,
			&d.Hash, &d.Size, &modS, &indexedS); err != nil {
			return nil, fmt.Errorf("docindex: scan: %w", err)
		}
		d.ModTime = parseTime(modS)
		d.IndexedAt = parseTime(indexedS)
		out = append(out, d)
	}
	return out, rows.Err()
}

// Count returns the number of indexed docs for a kind (empty kind = all kinds).
// Cheaper than List+len since it loads no bodies.
func (s *Store) Count(ctx context.Context, kind string) (int, error) {
	q := `SELECT COUNT(*) FROM authored_docs`
	var args []any
	if strings.TrimSpace(kind) != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("docindex: count: %w", err)
	}
	return n, nil
}

// Get returns one indexed doc by (kind, name), or ErrNotFound.
func (s *Store) Get(ctx context.Context, kind, name string) (Doc, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT kind, name, path, headline, body, hash, size, mod_time, indexed_at
		   FROM authored_docs WHERE kind = ? AND name = ?`, kind, name)
	var (
		d              Doc
		modS, indexedS string
	)
	err := row.Scan(&d.Kind, &d.Name, &d.Path, &d.Headline, &d.Body,
		&d.Hash, &d.Size, &modS, &indexedS)
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, ErrNotFound
	}
	if err != nil {
		return Doc{}, fmt.Errorf("docindex: get: %w", err)
	}
	d.ModTime = parseTime(modS)
	d.IndexedAt = parseTime(indexedS)
	return d, nil
}

// ErrNotFound is returned when a Get misses.
var ErrNotFound = errors.New("docindex: not found")

// --- internals ---

type fileInfo struct {
	path string
	size int64
	mod  time.Time
}

type indexedMeta struct {
	size int64
	mod  time.Time
}

// walkMarkdown returns every .md file under dir (recursively). A non-existent
// dir yields an empty set (not an error) so an unconfigured kind is benign.
func walkMarkdown(dir string) ([]fileInfo, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	var out []fileInfo
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		out = append(out, fileInfo{path: abs, size: fi.Size(), mod: fi.ModTime().UTC()})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

func (s *Store) indexedPaths(ctx context.Context, kind string) (map[string]indexedMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, size, mod_time FROM authored_docs WHERE kind = ?`, kind)
	if err != nil {
		return nil, fmt.Errorf("docindex: indexed paths: %w", err)
	}
	defer rows.Close()
	out := map[string]indexedMeta{}
	for rows.Next() {
		var (
			path, modS string
			size       int64
		)
		if err := rows.Scan(&path, &size, &modS); err != nil {
			return nil, err
		}
		out[path] = indexedMeta{size: size, mod: parseTime(modS)}
	}
	return out, rows.Err()
}

func (s *Store) upsert(ctx context.Context, kind string, fi fileInfo, now time.Time) error {
	body, err := os.ReadFile(fi.path)
	if err != nil {
		return fmt.Errorf("docindex: read %s: %w", fi.path, err)
	}
	sum := sha256.Sum256(body)
	d := Doc{
		Kind:     kind,
		Name:     strings.TrimSuffix(filepath.Base(fi.path), filepath.Ext(fi.path)),
		Path:     fi.path,
		Headline: headline(string(body)),
		Body:     string(body),
		Hash:     hex.EncodeToString(sum[:]),
		Size:     fi.size,
		ModTime:  fi.mod,
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO authored_docs (kind, name, path, headline, body, hash, size, mod_time, indexed_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(kind, path) DO UPDATE SET
            name=excluded.name, headline=excluded.headline, body=excluded.body,
            hash=excluded.hash, size=excluded.size, mod_time=excluded.mod_time,
            indexed_at=excluded.indexed_at`,
		d.Kind, d.Name, d.Path, d.Headline, d.Body, d.Hash, d.Size,
		fi.mod.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("docindex: upsert %s: %w", fi.path, err)
	}
	return nil
}

func (s *Store) delete(ctx context.Context, kind, path string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM authored_docs WHERE kind = ? AND path = ?`, kind, path); err != nil {
		return fmt.Errorf("docindex: delete %s: %w", path, err)
	}
	return nil
}

// headline returns the first meaningful line of a markdown body — the first
// non-blank line after any YAML frontmatter, with a leading "# " heading marker
// stripped. Empty for an empty body.
func headline(body string) string {
	lines := strings.Split(body, "\n")
	i := 0
	// Skip a leading YAML frontmatter block (--- … ---).
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				i = j + 1
				break
			}
		}
	}
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(t, "#"))
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
