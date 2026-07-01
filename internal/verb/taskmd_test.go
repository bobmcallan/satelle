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

// TestSyncTasks covers the file<->store reconciliation for tasks-as-substrate
// (sty_c1f9e74c): a store-only task is ADOPTED to a file (the legacy-DB migration
// path), a hand-authored file is INGESTED into the store (the file is the source
// of truth), and a repeat sync with no changes is a no-op (so the continuous
// serve watcher doesn't churn the store).
func TestSyncTasks(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	dir := t.TempDir()
	verb.SetTaskDir(dir)
	defer verb.SetTaskDir("")

	ctx := context.Background()
	now := time.Now()

	// Adopt: a store-only task (no file) gets its work-definition file written.
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindTask, Title: "Legacy", Body: "do x; verify x",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, mig, err := verb.SyncTasks(ctx, db.Stories, now); err != nil {
		t.Fatalf("sync: %v", err)
	} else if mig != 1 {
		t.Fatalf("migrated = %d, want 1", mig)
	}
	if _, err := os.Stat(filepath.Join(dir, it.ID+".md")); err != nil {
		t.Errorf("legacy store task was not adopted to a file: %v", err)
	}

	// Ingest: a hand-authored task file appears in the store.
	manual := "---\nid: tsk_hand01\nkind: task\nstatus: backlog\n---\n\n# Hand authored\n\nAudit; verify.\n"
	if err := os.WriteFile(filepath.Join(dir, "tsk_hand01.md"), []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}
	if idx, _, err := verb.SyncTasks(ctx, db.Stories, now); err != nil {
		t.Fatalf("sync2: %v", err)
	} else if idx < 1 {
		t.Errorf("indexed = %d, want >= 1", idx)
	}
	got, err := db.Stories.Get(ctx, "tsk_hand01")
	if err != nil {
		t.Fatalf("get hand task: %v", err)
	}
	if got.Title != "Hand authored" || got.Kind != workitem.KindTask {
		t.Errorf("ingested task = %+v", got)
	}

	// Idempotent: a repeat sync with no changes upserts and migrates nothing.
	if idx, mig, err := verb.SyncTasks(ctx, db.Stories, now.Add(time.Second)); err != nil {
		t.Fatalf("sync3: %v", err)
	} else if idx != 0 || mig != 0 {
		t.Errorf("re-sync should be a no-op; got indexed=%d migrated=%d", idx, mig)
	}
}
