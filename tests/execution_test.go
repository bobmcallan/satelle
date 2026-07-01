//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyTaskExecSubstrate copies this repo's task-execution substrate (the
// workflow + its two gate rubrics) into a temp repo, so an e2e can drive a real
// execution through the shipped lifecycle (the workflow is project substrate, not
// an embedded default). Reviewers are stubbed separately (stubReviewerAccept).
func copyTaskExecSubstrate(t *testing.T, repo string) {
	t.Helper()
	wd, err := os.Getwd() // = <repoRoot>/tests when `go test ./tests/...`
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(wd)
	files := map[string]string{
		filepath.Join(root, ".satelle", "workflows", "satelle-task-workflow.md"):            filepath.Join(repo, ".satelle", "workflows", "satelle-task-workflow.md"),
		filepath.Join(root, ".satelle", "skills", "satelle-task-validate-before-review.md"): filepath.Join(repo, ".satelle", "skills", "satelle-task-validate-before-review.md"),
		filepath.Join(root, ".satelle", "skills", "satelle-task-validate-after-review.md"):  filepath.Join(repo, ".satelle", "skills", "satelle-task-validate-after-review.md"),
	}
	for src, dst := range files {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read substrate %s: %v", src, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write substrate %s: %v", dst, err)
		}
	}
}

// TestExecutionLifecycleE2E drives a task execution through the full gated
// lifecycle (sty_2e6c39b8): backlog -> in_progress (validate-before) ->
// done (validate-after), with the execution's file frontmatter + op-log updated
// on accept. It then spawns a SECOND execution as the "re-run" (a new item, not a
// backward move) and asserts the first execution's `done` is terminal — a
// done -> in_progress transition is rejected (satelle-done-is-last).
func TestExecutionLifecycleE2E(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)    // every gate reviewer accepts (hermetic)
	copyTaskExecSubstrate(t, repo) // the task-execution workflow + gate rubrics
	mustRun(t, testBin, repo, "reindex")

	tasksDir := filepath.Join(repo, ".satelle", "tasks")

	tid := extractID(mustRun(t, testBin, repo, "task", "create",
		"--title", "Runnable", "--body", "ACTION: do the thing. VERIFICATION: it is done."), "tsk_")
	eid := extractID(mustRun(t, testBin, repo, "execution", "create", "--parent", tid,
		"--title", "Run 1", "--body", "ACTION: do it. VERIFICATION: done."), "exe_")
	if tid == "" || eid == "" {
		t.Fatalf("missing ids: task=%q exec=%q", tid, eid)
	}

	// Drive the run through both gates to terminal done.
	mustRun(t, testBin, repo, "execution", "set", eid, "--status", "in_progress")
	mustRun(t, testBin, repo, "execution", "set", eid, "--status", "done")

	// The execution's file frontmatter reflects done (the file is the source of truth).
	data, err := os.ReadFile(filepath.Join(tasksDir, tid, eid+".md"))
	if err != nil {
		t.Fatalf("execution file gone: %v", err)
	}
	if !strings.Contains(string(data), "status: done") {
		t.Errorf("execution frontmatter not updated to done:\n%s", data)
	}
	// The op-log records the transitions.
	oplog, _ := os.ReadFile(filepath.Join(repo, ".satelle", "logs", "operations.log"))
	if !strings.Contains(string(oplog), eid) || !strings.Contains(string(oplog), "in_progress -> done") {
		t.Errorf("op-log missing the execution transition:\n%s", oplog)
	}

	// Re-run = a NEW execution (not a backward move of the first).
	eid2 := extractID(mustRun(t, testBin, repo, "execution", "create", "--parent", tid,
		"--title", "Run 2", "--body", "ACTION: do it again. VERIFICATION: done."), "exe_")
	if eid2 == "" || eid2 == eid {
		t.Fatalf("re-run should be a distinct new execution; got %q (first %q)", eid2, eid)
	}

	// done is terminal: a done -> in_progress transition is rejected (no such edge).
	if out, err := run(t, testBin, repo, "execution", "set", eid, "--status", "in_progress"); err == nil {
		t.Errorf("done must be terminal — a backward transition should be rejected, got:\n%s", out)
	}
}

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
