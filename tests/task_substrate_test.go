//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskFilesAreSourceOfTruth proves tasks are authored substrate (sty_c1f9e74c):
// creating a task writes its .satelle/tasks/tsk_<id>.md work-definition file; a
// store task whose file was removed is re-adopted on `index` (the migration path
// for legacy DB-only tasks); a hand-authored task file is ingested into the store
// by `reindex` (the FILE is the source of truth); and `satelle task validate` covers the
// task files. This is NOT the removed story DB->disk mirror (sty_fa1e02e1) — there
// the DB was primary; here the file is primary.
func TestTaskFilesAreSourceOfTruth(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo) // baseline create gate is active — keep hermetic

	tasksDir := filepath.Join(repo, ".satelle", "tasks")

	// 1. Creating a task writes its work-definition file.
	out := mustRun(t, testBin, repo, "task", "create", "--title", "Alpha task", "--body", "Do the thing; verify it is done.")
	id := extractID(out, "tsk_")
	if id == "" {
		t.Fatalf("no task id in create output: %s", out)
	}
	file := filepath.Join(tasksDir, id+".md")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("task file %s not written: %v", file, err)
	}
	if !strings.Contains(string(data), "Alpha task") || !strings.Contains(string(data), "type: task") {
		t.Errorf("task file missing expected content:\n%s", data)
	}

	// 1b. Editing the task via `task set` updates the SAME work-definition file (AC2).
	mustRun(t, testBin, repo, "task", "set", id, "--title", "Alpha renamed", "--body", "New action; verify it ran.")
	edited, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("task file gone after set: %v", err)
	}
	if !strings.Contains(string(edited), "Alpha renamed") || !strings.Contains(string(edited), "New action; verify it ran.") {
		t.Errorf("`task set` did not update the on-disk file:\n%s", edited)
	}

	// 2. Remove the file, re-index -> the store task is re-adopted (migration path).
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")
	if _, err := os.Stat(file); err != nil {
		t.Errorf("index should re-adopt a store task lacking a file (legacy-DB migration): %v", err)
	}

	// 3. Hand-author a task file -> index ingests it into the store (file is source).
	manual := "---\nid: tsk_manual01\ntype: task\nstatus: backlog\n---\n\n# Hand authored\n\nAudit the thing; verify the audit ran.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "tsk_manual01.md"), []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")
	list := mustRun(t, testBin, repo, "task", "list")
	if !strings.Contains(list, "tsk_manual01") || !strings.Contains(list, "Hand authored") {
		t.Errorf("hand-authored task file was not ingested into the store:\n%s", list)
	}

	// 4. `satelle task validate` covers the task files.
	vout := mustRun(t, testBin, repo, "task", "validate")
	if !strings.Contains(vout, "tasks/tsk_manual01") {
		t.Errorf("satelle task validate did not cover the task file:\n%s", vout)
	}
}
