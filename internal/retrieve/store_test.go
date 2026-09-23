package retrieve

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPutDedupSameBytesSameStory(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	r1, err := s.Put(ctx, "sty_a", []byte("hello world"), now)
	if err != nil {
		t.Fatalf("put 1: %v", err)
	}
	if r1.Existed {
		t.Fatalf("first put: Existed = true, want false")
	}
	r2, err := s.Put(ctx, "sty_a", []byte("hello world"), now)
	if err != nil {
		t.Fatalf("put 2: %v", err)
	}
	if r1.Hash != r2.Hash {
		t.Fatalf("hash changed across dedup puts: %q vs %q", r1.Hash, r2.Hash)
	}
	if !r2.Existed {
		t.Fatalf("second put: Existed = false, want true")
	}

	var blobRows, refRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM retrieval`).Scan(&blobRows); err != nil {
		t.Fatal(err)
	}
	if blobRows != 1 {
		t.Errorf("retrieval row count = %d, want 1", blobRows)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM retrieval_refs`).Scan(&refRows); err != nil {
		t.Fatal(err)
	}
	if refRows != 1 {
		t.Errorf("retrieval_refs row count = %d, want 1 (same story re-Put must not duplicate the ref)", refRows)
	}
}

func TestPutCrossStoryDedupTwoRefsOneBlob(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	content := []byte("shared content")
	r1, err := s.Put(ctx, "sty_a", content, now)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Put(ctx, "sty_b", content, now)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Hash != r2.Hash {
		t.Fatalf("cross-story dedup produced different hashes: %q vs %q", r1.Hash, r2.Hash)
	}
	if !r2.Existed {
		t.Fatalf("second story's put: Existed = false, want true")
	}
	var blobRows, refRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM retrieval`).Scan(&blobRows)
	_ = db.QueryRow(`SELECT COUNT(*) FROM retrieval_refs`).Scan(&refRows)
	if blobRows != 1 {
		t.Errorf("retrieval row count = %d, want 1", blobRows)
	}
	if refRows != 2 {
		t.Errorf("retrieval_refs row count = %d, want 2 (one per story)", refRows)
	}
}

func TestPutAndGetRoundTripBinaryAndEmpty(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, content := range [][]byte{
		[]byte("plain text"),
		{0x00, 0xff, 0x10, 0x00, 0xab},
		{},
	} {
		ref, err := s.Put(ctx, "sty_a", content, now)
		if err != nil {
			t.Fatalf("put %v: %v", content, err)
		}
		got, err := s.Get(ctx, ref.Hash)
		if err != nil {
			t.Fatalf("get %v: %v", content, err)
		}
		if len(got) != len(content) {
			t.Fatalf("round trip length mismatch: got %d bytes, want %d", len(got), len(content))
		}
		for i := range content {
			if got[i] != content[i] {
				t.Fatalf("round trip byte mismatch at %d: got %x want %x", i, got[i], content[i])
			}
		}
	}
}

func TestGetUnknownHash(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	if _, err := s.Get(context.Background(), "0000000000000000000000"); err != ErrNotFound {
		t.Fatalf("Get(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestPruneKeepDaysZeroKeepsEverything(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	ref, _ := s.Put(ctx, "sty_old", []byte("x"), now)

	res, err := s.Prune(ctx, now, 0, func(string, time.Time, int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if res.RefsDeleted != 0 || res.BlobsDeleted != 0 {
		t.Fatalf("keepDays=0 pruned something: %+v", res)
	}
	if _, err := s.Get(ctx, ref.Hash); err != nil {
		t.Fatalf("blob missing after no-op prune: %v", err)
	}
}

func TestPruneTerminalOlderThanDeletesRefAndOrphanedBlob(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	ref, _ := s.Put(ctx, "sty_terminal_old", []byte("expired"), now)

	res, err := s.Prune(ctx, now, 30, func(storyID string, _ time.Time, _ int) bool {
		return storyID == "sty_terminal_old"
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RefsDeleted != 1 || res.BlobsDeleted != 1 {
		t.Fatalf("prune result = %+v, want 1 ref and 1 blob deleted", res)
	}
	if _, err := s.Get(ctx, ref.Hash); err != ErrNotFound {
		t.Fatalf("blob should be gone after its only ref expired: err = %v", err)
	}
}

// TestPruneRecentTerminalIsKept uses an age-real predicate (terminal is
// unconditionally true; only the elapsed-time check can save the ref) to
// prove a too-young ref survives on recency alone — the axis
// TestPruneNonTerminalIsKept below does NOT exercise.
func TestPruneRecentTerminalIsKept(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	created := now
	ref, _ := s.Put(ctx, "sty_recent", []byte("recent"), created)

	// Terminal unconditionally true — only the elapsed-time check (mirroring
	// verb.pruneRetrievalStore's real now.Sub(item.UpdatedAt) comparison) can
	// save this ref, and it does, because created == now.
	isTerminalOlderThan := func(_ string, evalNow time.Time, keepDays int) bool {
		return evalNow.Sub(created) > time.Duration(keepDays)*24*time.Hour
	}
	res, err := s.Prune(ctx, now, 30, isTerminalOlderThan)
	if err != nil {
		t.Fatal(err)
	}
	if res.RefsDeleted != 0 || res.BlobsDeleted != 0 {
		t.Fatalf("recent terminal story pruned: %+v", res)
	}
	if _, err := s.Get(ctx, ref.Hash); err != nil {
		t.Fatalf("blob missing after keep: %v", err)
	}
}

// TestPruneNonTerminalIsKept plants a ref old enough that an age-only check
// would prune it, then uses a predicate that reports non-terminal
// unconditionally — proving status, not age, is what keeps it. This is the
// opposite axis from TestPruneRecentTerminalIsKept above (real end-to-end
// coverage of both, wired together, lives in
// verb.TestPruneRetrievalStoreRetention).
func TestPruneNonTerminalIsKept(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)
	ref, _ := s.Put(ctx, "sty_active", []byte("active"), old)

	// isTerminalOlderThan reports false for a non-terminal story regardless of age.
	res, err := s.Prune(ctx, now, 1, func(string, time.Time, int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if res.RefsDeleted != 0 {
		t.Fatalf("non-terminal story's ref pruned: %+v", res)
	}
	if _, err := s.Get(ctx, ref.Hash); err != nil {
		t.Fatalf("blob missing after keep: %v", err)
	}
}

// TestPruneCrossStorySurvivorKeepsBlob: two stories reference one blob; only
// one is expired. The expired ref is removed but the blob survives because the
// live sibling's ref still points at it, and the surviving story's retrieve
// still works.
func TestPruneCrossStorySurvivorKeepsBlob(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	content := []byte("shared across two stories")
	ref, _ := s.Put(ctx, "sty_expired", content, now)
	if _, err := s.Put(ctx, "sty_live", content, now); err != nil {
		t.Fatal(err)
	}

	res, err := s.Prune(ctx, now, 30, func(storyID string, _ time.Time, _ int) bool {
		return storyID == "sty_expired"
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RefsDeleted != 1 {
		t.Fatalf("RefsDeleted = %d, want 1", res.RefsDeleted)
	}
	if res.BlobsDeleted != 0 {
		t.Fatalf("BlobsDeleted = %d, want 0 (sty_live still references it)", res.BlobsDeleted)
	}
	got, err := s.Get(ctx, ref.Hash)
	if err != nil {
		t.Fatalf("blob should survive via sty_live's ref: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("surviving blob content mismatch")
	}

	var refRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM retrieval_refs WHERE hash = ?`, ref.Hash).Scan(&refRows)
	if refRows != 1 {
		t.Fatalf("remaining ref count = %d, want 1 (sty_live only)", refRows)
	}
}

func TestPruneOrphanBlobWithNoRefsIsKept(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	// Put with an empty storyID leaves the blob with zero refs (Put's ref
	// insert is skipped for empty storyID) — this is the orphan case.
	ref, err := s.Put(ctx, "", []byte("orphan"), now)
	if err != nil {
		t.Fatal(err)
	}
	var refRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM retrieval_refs WHERE hash = ?`, ref.Hash).Scan(&refRows)
	if refRows != 0 {
		t.Fatalf("expected zero refs for empty storyID put, got %d", refRows)
	}

	res, err := s.Prune(ctx, now, 1, func(string, time.Time, int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if res.BlobsDeleted != 0 {
		t.Fatalf("orphan blob with no ref was pruned: %+v", res)
	}
	if _, err := s.Get(ctx, ref.Hash); err != nil {
		t.Fatalf("orphan blob missing after prune: %v", err)
	}
}
