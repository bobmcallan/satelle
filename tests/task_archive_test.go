//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskArchiveEndToEnd drives the real binary to prove the task archive verb
// (sty_cd209b8a): `satelle task archive <id>` drops the task from the default
// `task list`, keeps it readable via `task get` (archived=true), MOVES the header
// to .satelle/backups/tasks/, and reindex never resurrects it.
func TestTaskArchiveEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Author a task header on disk, index it.
	tasksDir := filepath.Join(repo, ".satelle", "tasks")
	body := "---\nid: tsk_arch01\ntype: task\nstatus: backlog\n---\n\n# Superseded\n\nACTION: do X. VERIFICATION: X done.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "tsk_arch01.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	// It lists before archival.
	if out := mustRun(t, testBin, repo, "task", "list"); !strings.Contains(out, "tsk_arch01") {
		t.Fatalf("task not listed before archive:\n%s", out)
	}

	// Archive it.
	mustRun(t, testBin, repo, "task", "archive", "tsk_arch01")

	// Dropped from the default list.
	if out := mustRun(t, testBin, repo, "task", "list"); strings.Contains(out, "tsk_arch01") {
		t.Errorf("archived task still in the default list:\n%s", out)
	}
	// Still readable via get, marked archived.
	var got map[string]any
	if err := json.Unmarshal([]byte(mustRun(t, testBin, repo, "task", "get", "tsk_arch01")), &got); err != nil {
		t.Fatalf("task get json: %v", err)
	}
	if got["archived"] != true {
		t.Errorf("task get did not report archived=true: %+v", got)
	}
	// Header moved off the tasks dir into a backup.
	if _, err := os.Stat(filepath.Join(tasksDir, "tsk_arch01.md")); !os.IsNotExist(err) {
		t.Error("archived header still present in .satelle/tasks/")
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot(t, repo), "backups", "tasks")); err != nil {
		t.Errorf("archive did not write a backup under runtime backups/tasks/: %v", err)
	}

	// Reindex must not resurrect the archived record to a file, and stays green.
	mustRun(t, testBin, repo, "reindex")
	if _, err := os.Stat(filepath.Join(tasksDir, "tsk_arch01.md")); !os.IsNotExist(err) {
		t.Error("reindex resurrected the archived task header")
	}
	if out := mustRun(t, testBin, repo, "task", "list"); strings.Contains(out, "tsk_arch01") {
		t.Errorf("archived task reappeared after reindex:\n%s", out)
	}
}
