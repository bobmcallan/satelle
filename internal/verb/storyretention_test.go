package verb_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// wireStories opens a temp store, wires it, and points the story dir at a temp
// path — returning the store and storyDir so a retention test can seed stories
// with controlled update times and inspect the attachment dirs.
func wireStories(t *testing.T) (*workitem.Store, string) {
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
	verb.SetLedgerStore(db.Ledger)
	verb.SetStoryDir(storyDir)
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetStoryDir("")
		verb.SetStoryRetention(0, 0)
	})
	return db.Stories, storyDir
}

// seedStory creates a story with an explicit status and update time, plus an
// attachment dir with one file — the retention unit's fixture.
func seedStory(t *testing.T, st *workitem.Store, storyDir, status string, updated time.Time) string {
	t.Helper()
	ctx := context.Background()
	it, err := st.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "S", Status: status}, updated)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(storyDir, it.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commit-summary.md"), []byte("evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	return it.ID
}

func dirExists(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// TestStoryRetentionCountAndOverride proves the count policy keeps the N
// most-recently-updated closed dirs and prunes the rest to a backup, while a
// non-terminal story's dir is ALWAYS kept (sty_aba7200c).
func TestStoryRetentionCountAndOverride(t *testing.T) {
	st, storyDir := wireStories(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	oldest := seedStory(t, st, storyDir, workitem.StatusDone, base)
	mid := seedStory(t, st, storyDir, "cancelled", base.Add(time.Hour))
	newest := seedStory(t, st, storyDir, workitem.StatusDone, base.Add(2*time.Hour))
	// A non-terminal (backlog) story with an ANCIENT update — must never prune.
	openOld := seedStory(t, st, storyDir, workitem.StatusBacklog, base.Add(-100*24*time.Hour))

	verb.SetStoryRetention(2, 0) // keep the 2 most-recent closed dirs
	if _, _, err := verb.SyncStoryBacklog(ctx, st, base.Add(3*time.Hour)); err != nil {
		t.Fatalf("SyncStoryBacklog: %v", err)
	}

	if !dirExists(t, filepath.Join(storyDir, newest)) || !dirExists(t, filepath.Join(storyDir, mid)) {
		t.Error("the 2 most-recent closed dirs must be kept")
	}
	if dirExists(t, filepath.Join(storyDir, oldest)) {
		t.Error("the oldest closed dir (beyond N) must be pruned")
	}
	if !dirExists(t, filepath.Join(storyDir, openOld)) {
		t.Error("a non-terminal story's dir must always be kept, regardless of age/count")
	}
	// The pruned dir was MOVED to a backup, not deleted.
	backups := filepath.Join(filepath.Dir(storyDir), "backups", "stories")
	if findFile(t, backups, "commit-summary.md") == "" {
		t.Errorf("pruned dir's evidence not found under the backup dir %s", backups)
	}
}

// TestStoryRetentionAge proves the age policy prunes a closed dir older than the
// horizon while keeping a recent one, and that no policy is a no-op.
func TestStoryRetentionAge(t *testing.T) {
	st, storyDir := wireStories(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	stale := seedStory(t, st, storyDir, workitem.StatusDone, now.Add(-40*24*time.Hour))
	fresh := seedStory(t, st, storyDir, workitem.StatusDone, now.Add(-5*24*time.Hour))

	// No policy configured → nothing pruned.
	verb.SetStoryRetention(0, 0)
	if _, _, err := verb.SyncStoryBacklog(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if !dirExists(t, filepath.Join(storyDir, stale)) {
		t.Error("with no policy, no dir may be pruned")
	}

	// Age horizon of 30 days → the 40-day-old dir prunes, the 5-day-old stays.
	verb.SetStoryRetention(0, 30)
	if _, _, err := verb.SyncStoryBacklog(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if dirExists(t, filepath.Join(storyDir, stale)) {
		t.Error("a closed dir older than the age horizon must be pruned")
	}
	if !dirExists(t, filepath.Join(storyDir, fresh)) {
		t.Error("a closed dir within the age horizon must be kept")
	}
}
