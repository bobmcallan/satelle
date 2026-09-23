// Package retrieve is the CCR (compress-cache-retrieve) store: a compressor
// that drops content stores the original under a content hash here and leaves
// a marker (see marker.go) in its condensed output. `satelle retrieve <hash>`
// later returns the exact original bytes — the ledger and gate evidence stay
// complete even though the model read a condensed view.
//
// The blob table (retrieval) is deduplicated by content hash; the link table
// (retrieval_refs) records which stories produced/reference a given blob. A
// blob is pruned only once every referencing story is terminal and past the
// configured retention — a story that pruned first never deletes bytes a live
// sibling still references (sty_b0577532).
package retrieve

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Get when no blob is stored under hash.
var ErrNotFound = errors.New("retrieve: not found")

// hashLen is the number of hex characters a stored hash key carries — the
// first 24 hex chars (12 bytes) of the content's sha256, mirroring Headroom's
// blake3 hex[:24] truncation length without a new hash dependency.
const hashLen = 24

const schema = `
CREATE TABLE IF NOT EXISTS retrieval (
    hash       TEXT PRIMARY KEY,
    bytes      BLOB NOT NULL,
    size       INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS retrieval_refs (
    hash       TEXT NOT NULL,
    story_id   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (hash, story_id)
);
CREATE INDEX IF NOT EXISTS idx_retrieval_refs_story ON retrieval_refs(story_id);
`

// Migrate creates the retrieval tables. Idempotent.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("retrieve: migrate: %w", err)
	}
	return nil
}

// Store wraps the retrieval/retrieval_refs tables against a shared sqlite handle.
type Store struct{ db *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Ref is the outcome of a Put: the content's key, its ready-to-embed marker,
// and whether the blob already existed under this hash before this call.
type Ref struct {
	Hash    string
	Marker  string
	Existed bool
}

// Hash returns the storage key for content: the first 24 hex characters of its
// sha256 digest.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:hashLen]
}

// Put stores content under its content hash and links it to storyID, so a
// second story storing the identical bytes shares the one blob but gets its
// own ref (AC3 / cross-story dedup). Existed is true when the blob already had
// at least one row before this call — the write path never re-inserts the
// blob, only the ref.
func (s *Store) Put(ctx context.Context, storyID string, content []byte, now time.Time) (Ref, error) {
	if s == nil || s.db == nil {
		return Ref{}, errors.New("retrieve: store not configured")
	}
	hash := Hash(content)
	nowS := now.UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ref{}, fmt.Errorf("retrieve: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existed bool
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM retrieval WHERE hash = ?`, hash).Scan(new(int)); err == nil {
		existed = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Ref{}, fmt.Errorf("retrieve: check existing: %w", err)
	}

	if !existed {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO retrieval (hash, bytes, size, created_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(hash) DO NOTHING`,
			hash, content, len(content), nowS); err != nil {
			return Ref{}, fmt.Errorf("retrieve: insert blob: %w", err)
		}
	}
	if storyID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO retrieval_refs (hash, story_id, created_at) VALUES (?, ?, ?)
			 ON CONFLICT(hash, story_id) DO NOTHING`,
			hash, storyID, nowS); err != nil {
			return Ref{}, fmt.Errorf("retrieve: insert ref: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Ref{}, fmt.Errorf("retrieve: commit: %w", err)
	}
	return Ref{Hash: hash, Marker: Marker(hash), Existed: existed}, nil
}

// Get returns the exact original bytes stored under hash, or ErrNotFound.
func (s *Store) Get(ctx context.Context, hash string) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("retrieve: store not configured")
	}
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT bytes FROM retrieval WHERE hash = ?`, hash).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("retrieve: get: %w", err)
	}
	return b, nil
}

// IsTerminalOlderThan decides, for one ref's owning story, whether that ref is
// expired under the configured retention. The retrieve package holds no
// opinion about story status — the verb layer supplies this predicate so
// internal/retrieve never imports workitem (sty_b0577532 architecture review).
type IsTerminalOlderThan func(storyID string, now time.Time, keepDays int) bool

// PruneResult reports what Prune removed.
type PruneResult struct {
	RefsDeleted  int
	BlobsDeleted int
}

// Prune removes expired refs and any blob left with none. keepDays <= 0 is a
// no-op (retention "keep forever", the default) — no ref or blob is ever
// examined. A ref is expired when isTerminalOlderThan reports its owning story
// terminal and past keepDays; an expired ref is deleted, and the blob it
// pointed at is deleted only when NO ref remains for that hash. A blob with no
// ref at all (orphan — never referenced, or ref-less by construction) is never
// touched here: it had no expiring ref to trigger the check.
func (s *Store) Prune(ctx context.Context, now time.Time, keepDays int, isTerminalOlderThan IsTerminalOlderThan) (PruneResult, error) {
	var res PruneResult
	if s == nil || s.db == nil {
		return res, errors.New("retrieve: store not configured")
	}
	if keepDays <= 0 || isTerminalOlderThan == nil {
		return res, nil
	}

	rows, err := s.db.QueryContext(ctx, `SELECT hash, story_id FROM retrieval_refs`)
	if err != nil {
		return res, fmt.Errorf("retrieve: prune list refs: %w", err)
	}
	type ref struct{ hash, story string }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.hash, &r.story); err != nil {
			rows.Close()
			return res, fmt.Errorf("retrieve: prune scan ref: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, fmt.Errorf("retrieve: prune list refs: %w", err)
	}
	rows.Close()

	touched := map[string]bool{}
	for _, r := range refs {
		if !isTerminalOlderThan(r.story, now, keepDays) {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM retrieval_refs WHERE hash = ? AND story_id = ?`, r.hash, r.story); err != nil {
			return res, fmt.Errorf("retrieve: prune delete ref: %w", err)
		}
		res.RefsDeleted++
		touched[r.hash] = true
	}

	for hash := range touched {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM retrieval_refs WHERE hash = ?`, hash).Scan(&n); err != nil {
			return res, fmt.Errorf("retrieve: prune count refs: %w", err)
		}
		if n > 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM retrieval WHERE hash = ?`, hash); err != nil {
			return res, fmt.Errorf("retrieve: prune delete blob: %w", err)
		}
		res.BlobsDeleted++
	}
	return res, nil
}
