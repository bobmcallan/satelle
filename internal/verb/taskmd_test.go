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

// TestSyncExecutions covers the execution entity's file<->store reconciliation
// (sty_ef08ce2a): an execution materialises under its PARENT TASK's folder
// (<taskDir>/<tsk_id>/exe_*.md, not flat beside the header), a store-only
// execution is adopted to that folder, and a hand-authored run file in the
// folder is ingested and stamped with the folder's task id as its parent.
func TestSyncExecutions(t *testing.T) {
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

	// A task header + a store-only execution parented to it.
	task, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindTask, Title: "Parent"}, now)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindExecution, Title: "Run 1", Status: "backlog", ParentID: task.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := verb.SyncTasks(ctx, db.Stories, now); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// The execution materialises UNDER the task's folder, not flat.
	execFile := filepath.Join(dir, task.ID, exe.ID+".md")
	if _, err := os.Stat(execFile); err != nil {
		t.Errorf("execution not adopted to its per-task folder %s: %v", execFile, err)
	}
	if _, err := os.Stat(filepath.Join(dir, exe.ID+".md")); err == nil {
		t.Error("execution must NOT be written flat beside the task header")
	}

	// A hand-authored run file in the folder is ingested and re-parented to the folder.
	run := "---\nid: exe_hand01\ntype: execution\nstatus: in_progress\n---\n\n# Hand run\n\nACTION; VERIFICATION.\n"
	if err := os.WriteFile(filepath.Join(dir, task.ID, "exe_hand01.md"), []byte(run), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verb.SyncTasks(ctx, db.Stories, now.Add(time.Second)); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	got, err := db.Stories.Get(ctx, "exe_hand01")
	if err != nil {
		t.Fatalf("get hand execution: %v", err)
	}
	if got.Kind != workitem.KindExecution || got.ParentID != task.ID {
		t.Errorf("ingested execution = %+v, want kind=execution parent=%s", got, task.ID)
	}
}
