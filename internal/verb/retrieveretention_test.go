package verb_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/retrieve"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireStoriesAndRetrieve is wireStories (storyretention_test.go) plus the CCR
// retrieval store wired on the same db handle — SyncStoryBacklog's real
// pruneRetrievalStore predicate reads story status/updated_at through
// workStore, so a verb-level retention test needs both stores live together.
// StoryDir must be set too: SyncStoryBacklog no-ops entirely (never reaching
// its retrieval sweep) while storyDir is unset.
func wireStoriesAndRetrieve(t *testing.T) (*workitem.Store, *retrieve.Store) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	storyDir := filepath.Join(dir, ".satelle", "stories")
	if err := os.MkdirAll(storyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetRetrieveStore(db.Retrieve)
	verb.SetStoryDir(storyDir)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetRetrieveStore(nil)
		verb.SetStoryDir("")
		verb.SetRetrieveRetention(0)
	})
	return db.Stories, db.Retrieve
}

// TestPruneRetrievalStoreRetention proves the real predicate wired in
// internal/verb/retrieve.go pruneRetrievalStore — terminal AND older than
// retrieve_keep_days — end to end through SyncStoryBacklog, the only caller
// (storyokf.go). A recent-done story's blob, an old-in_progress story's blob,
// and a blob shared with a live sibling must all survive; only the old-done
// story's own blob is pruned.
func TestPruneRetrievalStoreRetention(t *testing.T) {
	st, rs := wireStoriesAndRetrieve(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	oldDone, err := st.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "old done", Status: workitem.StatusDone}, now.Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	recentDone, err := st.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "recent done", Status: workitem.StatusDone}, now.Add(-5*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	oldInProgress, err := st.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "old in progress", Status: workitem.StatusInProgress}, now.Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sharer, err := st.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "shares old-done's blob", Status: workitem.StatusInProgress}, now)
	if err != nil {
		t.Fatal(err)
	}

	ownRef, err := rs.Put(ctx, oldDone.ID, []byte("old done's own, unshared evidence"), now.Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	recentRef, err := rs.Put(ctx, recentDone.ID, []byte("recent done's evidence"), now.Add(-5*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	activeRef, err := rs.Put(ctx, oldInProgress.ID, []byte("old in-progress evidence"), now.Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// old-done ALSO references a second blob that a still-active sibling
	// references too — that blob must survive old-done's own ref expiring,
	// because the sharer's live ref still points at it (sty_b0577532 AC3
	// cross-story dedup + retention: a blob dies only once every ref does).
	sharedContent := []byte("evidence shared between old-done and a live sibling")
	sharedOldRef, err := rs.Put(ctx, oldDone.ID, sharedContent, now.Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sharedLiveRef, err := rs.Put(ctx, sharer.ID, sharedContent, now)
	if err != nil {
		t.Fatal(err)
	}
	if sharedLiveRef.Hash != sharedOldRef.Hash {
		t.Fatalf("shared content hashed differently: %s vs %s", sharedLiveRef.Hash, sharedOldRef.Hash)
	}

	// No retention configured → SyncStoryBacklog's sweep must not touch anything.
	verb.SetRetrieveRetention(0)
	if _, _, err := verb.SyncStoryBacklog(ctx, st, now); err != nil {
		t.Fatalf("SyncStoryBacklog (retention off): %v", err)
	}
	for _, ref := range []retrieve.Ref{ownRef, recentRef, activeRef, sharedOldRef} {
		if _, err := rs.Get(ctx, ref.Hash); err != nil {
			t.Fatalf("blob %s pruned with retention off: %v", ref.Hash, err)
		}
	}

	// 30-day retention → only old-done's OWN, unshared blob (no surviving ref) goes.
	verb.SetRetrieveRetention(30)
	if _, _, err := verb.SyncStoryBacklog(ctx, st, now); err != nil {
		t.Fatalf("SyncStoryBacklog (retention 30): %v", err)
	}
	if _, err := rs.Get(ctx, ownRef.Hash); !errors.Is(err, retrieve.ErrNotFound) {
		t.Fatalf("old-done's own blob should be gone, got err = %v", err)
	}
	if _, err := rs.Get(ctx, recentRef.Hash); err != nil {
		t.Fatalf("recent-terminal story's blob must be kept: %v", err)
	}
	if _, err := rs.Get(ctx, activeRef.Hash); err != nil {
		t.Fatalf("non-terminal (in_progress) story's blob must be kept regardless of age: %v", err)
	}
	// old-done's ref to the shared blob expired too, but the sharer's live ref
	// keeps the blob itself alive.
	if _, err := rs.Get(ctx, sharedLiveRef.Hash); err != nil {
		t.Fatalf("blob shared with a still-active story must survive the terminal sibling's expiry: %v", err)
	}
}
