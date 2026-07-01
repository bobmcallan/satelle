//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecutionEntity proves the task/execution split (sty_ef08ce2a): a task
// header is a flat file, while each EXECUTION is a separate item materialised
// UNDER its parent task's folder (.satelle/tasks/<tsk_id>/exe_*.md). A store-only
// execution is re-adopted on reindex, a hand-authored run file is ingested (the
// file is the source of truth), and the execution appears in `execution list`.
func TestExecutionEntity(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo) // baseline create gate is active — keep hermetic

	tasksDir := filepath.Join(repo, ".satelle", "tasks")

	// A task header (stays a flat file).
	tout := mustRun(t, testBin, repo, "task", "create", "--title", "Runnable task", "--body", "Do the thing; verify it is done.")
	tid := extractID(tout, "tsk_")
	if tid == "" {
		t.Fatalf("no task id in create output: %s", tout)
	}
	if _, err := os.Stat(filepath.Join(tasksDir, tid+".md")); err != nil {
		t.Fatalf("task header not written flat: %v", err)
	}

	// An execution created against the task materialises under the task's folder.
	eout := mustRun(t, testBin, repo, "execution", "create", "--parent", tid, "--title", "Run 1", "--body", "ACTION: run; VERIFICATION: it ran.")
	eid := extractID(eout, "exe_")
	if eid == "" {
		t.Fatalf("no execution id in create output: %s", eout)
	}
	execFile := filepath.Join(tasksDir, tid, eid+".md")
	if _, err := os.Stat(execFile); err != nil {
		t.Errorf("execution not materialised under its per-task folder %s: %v", execFile, err)
	}
	if _, err := os.Stat(filepath.Join(tasksDir, eid+".md")); err == nil {
		t.Error("execution must NOT be written flat beside the task header")
	}

	// Remove the run file, reindex -> the store execution is re-adopted (migration).
	if err := os.Remove(execFile); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")
	if _, err := os.Stat(execFile); err != nil {
		t.Errorf("reindex should re-adopt a store execution lacking a file: %v", err)
	}

	// Hand-author a second run file in the folder -> reindex ingests it (file<->index).
	run := "---\nid: exe_manual01\ntype: execution\nstatus: in_progress\n---\n\n# Hand run\n\nACTION; VERIFICATION.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, tid, "exe_manual01.md"), []byte(run), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")
	list := mustRun(t, testBin, repo, "execution", "list", "--parent", tid)
	if !strings.Contains(list, "exe_manual01") || !strings.Contains(list, eid) {
		t.Errorf("execution list did not reflect the ingested runs:\n%s", list)
	}
}
