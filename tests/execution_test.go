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

// TestExecutionCodedEntryGateRejects proves the coded begin-run gate
// (sty_3c1a2a9d): backlog -> in_progress is a deterministic structural check —
// no LLM verdict — that REFUSES a run whose parent task header is missing or
// structurally invalid, naming the problem. Reviewers are stubbed to accept, so
// any rejection observed here can only come from the coded check.
func TestExecutionCodedEntryGateRejects(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo) // every LLM gate accepts — rejections below are the coded check's
	copyTaskExecSubstrate(t, repo)
	mustRun(t, testBin, repo, "reindex")

	tid := extractID(mustRun(t, testBin, repo, "task", "create",
		"--title", "Runnable", "--body", "ACTION: do. VERIFICATION: done."), "tsk_")
	eid := extractID(mustRun(t, testBin, repo, "execution", "create", "--parent", tid,
		"--title", "Run 1", "--body", "ACTION: do it. VERIFICATION: done."), "exe_")
	if tid == "" || eid == "" {
		t.Fatalf("missing ids: task=%q exec=%q", tid, eid)
	}
	headerFile := filepath.Join(repo, ".satelle", "tasks", tid+".md")
	valid, err := os.ReadFile(headerFile)
	if err != nil {
		t.Fatal(err)
	}

	// Missing parent header: the gate refuses, naming the absent file.
	if err := os.Remove(headerFile); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, testBin, repo, "execution", "set", eid, "--status", "in_progress")
	if err == nil {
		t.Fatalf("gate must refuse a run with a missing parent header, got:\n%s", out)
	}
	if !strings.Contains(out, "does not exist") {
		t.Errorf("refusal should name the missing header, got:\n%s", out)
	}

	// Structurally invalid header (no `type: task`): the gate refuses, naming the problem.
	broken := strings.Replace(string(valid), "type: task\n", "", 1)
	if err := os.WriteFile(headerFile, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, testBin, repo, "execution", "set", eid, "--status", "in_progress")
	if err == nil {
		t.Fatalf("gate must refuse a run with a structurally invalid parent, got:\n%s", out)
	}
	if !strings.Contains(out, "type: task") {
		t.Errorf("refusal should name the structural problem, got:\n%s", out)
	}

	// Restore the valid header: the same edge now accepts (the coded check's accept branch).
	if err := os.WriteFile(headerFile, valid, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "execution", "set", eid, "--status", "in_progress")
}

// TestTaskHeaderRoutesToTaskWorkflow proves the kind-aware routing fix
// (sty_3c1a2a9d): an UNSTAMPED task header (authored category, no workflow: tag)
// resolves to satelle-task-workflow — driving it to in_progress hits the task
// workflow's coded entry gate, which refuses with the create-an-execution
// remedy (a header has no parent task). Before the fix the header fell through
// to the wildcard story workflow; the distinctive coded-gate message proves the
// governing workflow end-to-end via the real binary.
func TestTaskHeaderRoutesToTaskWorkflow(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)
	copyTaskExecSubstrate(t, repo)
	mustRun(t, testBin, repo, "reindex")

	tid := extractID(mustRun(t, testBin, repo, "task", "create",
		"--title", "Header only", "--body", "ACTION: definition. VERIFICATION: per run."), "tsk_")
	if tid == "" {
		t.Fatal("no task id")
	}
	out, err := run(t, testBin, repo, "task", "set", tid, "--status", "in_progress")
	if err == nil {
		t.Fatalf("driving a bare header must be refused by the task workflow's coded gate, got:\n%s", out)
	}
	if !strings.Contains(out, "execution create --parent") {
		t.Errorf("refusal should carry the create-an-execution remedy (proving satelle-task-workflow governs the header), got:\n%s", out)
	}
}

// TestInstallAliasesInit proves `satelle install` is a full alias of init
// (sty_77367228): it scaffolds a fresh repo identically, and help names it.
func TestInstallAliasesInit(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, testBin, repo, "install")
	for _, rel := range []string{".satelle/satelle.toml", ".satelle/satelle.db", ".satelle/tasks/README.md"} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("install did not scaffold %s: %v", rel, err)
		}
	}
	if !strings.Contains(out, "Ready.") {
		t.Errorf("install should report like init:\n%s", out)
	}
	if help := mustRun(t, testBin, repo, "init", "--help"); !strings.Contains(help, "install") {
		t.Errorf("init help should list the install alias:\n%s", help)
	}
}

// TestInitScaffoldsTasksDir proves `satelle init` scaffolds .satelle/tasks/ with
// a README keep-file but seeds NO example task (sty_04ec1fe6): a fresh repo
// starts with an empty tasks dir, and a second init reports the dir as already
// present ("=", not "+").
func TestInitScaffoldsTasksDir(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, ".satelle/tasks/") {
		t.Errorf("init report missing the tasks scaffold:\n%s", out)
	}
	if strings.Contains(out, "tsk_example1") {
		t.Errorf("init must not seed or report an example task:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/README.md")); err != nil {
		t.Errorf("init did not scaffold the tasks README keep-file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/tsk_example1.md")); err == nil {
		t.Error("init must not seed an example task (tsk_example1.md)")
	}
	// A second init reports the existing tasks dir as present ("="), not created.
	out2 := mustRun(t, testBin, repo, "init")
	if strings.Contains(out2, "+ .satelle/tasks/") {
		t.Errorf("second init reported the existing tasks dir as created:\n%s", out2)
	}
	// task validate is green over an empty (example-free) tasks dir.
	mustRun(t, testBin, repo, "reindex")
	if v := mustRun(t, testBin, repo, "task", "validate"); !strings.Contains(v, "failed 0") {
		t.Errorf("empty tasks dir failed validate:\n%s", v)
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
