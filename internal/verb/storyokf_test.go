package verb_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestSyncStoryBacklog covers the read-only OKF backlog reference: open stories
// are rendered as generated files + index/log, terminal (done/cancelled) stories
// are excluded, the legacy flat sty_*.md mirror leftovers are pruned, and
// per-story attachment subdirs survive. The DB stays authoritative — this is a
// disposable view.
func TestSyncStoryBacklog(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	dir := t.TempDir()
	verb.SetStoryDir(dir)
	defer verb.SetStoryDir("")
	ctx := context.Background()
	now := time.Now()

	open1, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Open one", Body: "first open body"}, now)
	_, _ = db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Open two"}, now)
	doneIt, _ := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Done one"}, now)
	if _, err := db.Stories.SetStatus(ctx, doneIt.ID, workitem.StatusDone, now); err != nil {
		t.Fatal(err)
	}

	// plant a legacy flat mirror leftover + a live attachment subdir.
	if err := os.WriteFile(filepath.Join(dir, "sty_deadbeef.md"), []byte("# stale legacy mirror"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, open1.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, open1.ID, "note.md"), []byte("attachment"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := verb.SyncStoryBacklog(ctx, db.Stories, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("materialized %d stories, want 2 (open only)", n)
	}
	// open stories rendered; done excluded.
	if _, err := os.Stat(filepath.Join(dir, open1.ID+".md")); err != nil {
		t.Errorf("open story file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, doneIt.ID+".md")); !os.IsNotExist(err) {
		t.Errorf("done story was NOT excluded from the backlog reference")
	}
	// legacy leftover pruned; attachment subdir + its file preserved.
	if _, err := os.Stat(filepath.Join(dir, "sty_deadbeef.md")); !os.IsNotExist(err) {
		t.Errorf("legacy flat mirror leftover was not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, open1.ID, "note.md")); err != nil {
		t.Errorf("per-story attachment subdir was wrongly removed: %v", err)
	}
	// index.md lists the open backlog.
	idx, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "# Backlog") || !strings.Contains(string(idx), open1.ID+".md") {
		t.Errorf("index.md is not a backlog reference:\n%s", idx)
	}
	if strings.Contains(string(idx), doneIt.ID) {
		t.Errorf("index.md includes the done story")
	}
}
