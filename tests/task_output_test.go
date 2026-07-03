//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dispatchTaskWorkflow overrides the seeded task workflow so the in_progress run
// step is DISPATCHED to the named agent "runner" — exercising the dispatched
// output-capture path (sty_890b86cb). The entry gate stays the coded
// validate-before check; the exit gate is left off the driven path (we only need
// backlog → in_progress, where dispatch fires).
const dispatchTaskWorkflow = `---
name: satelle-task-workflow
type: workflow
description: task-execution lifecycle whose run step is dispatched to a named agent
applies_to: ["execution", "task"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=runner]
  done [shape=Msquare]
  backlog -> in_progress [reviewer_skill="satelle-task-validate-before-review"]
  in_progress -> done [reviewer_skill="satelle-task-validate-after-review"]
}
` + "```\n"

// runnerScript echoes distinctive run output on stdout (which satelle captures at
// the dispatch site) after consuming the payload on stdin.
const runnerScript = `#!/bin/sh
cat > /dev/null
echo "RUNNER-DID-THE-WORK"
echo "VERIFICATION satisfied"
`

// authoredTask writes a valid task header (ACTION + VERIFICATION) the coded entry
// gate accepts.
func writeAuthoredTask(t *testing.T, repo, id string) {
	t.Helper()
	body := "---\nid: " + id + "\ntype: task\nstatus: backlog\n---\n\n# " + id + "\n\nACTION: produce output. VERIFICATION: the run output is recorded.\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "tasks", id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExecutionRecordAndAttachEndToEnd drives the real binary: the in-loop
// `execution record` verb writes an OKF run-output doc under the task folder, a
// `story attach` on a TASK lands under .satelle/tasks/ (kind-resolved root), and
// reindex stays green over the collected output (sty_890b86cb).
func TestExecutionRecordAndAttachEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeAuthoredTask(t, repo, "tsk_out01")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_out01", "--title", "run 1")
	exe := extractID(out, "exe_")
	if exe == "" {
		t.Fatalf("no execution id in:\n%s", out)
	}

	// In-loop capture: record the run output via the verb.
	mustRun(t, testBin, repo, "execution", "record", exe, "--output", "did the work; verification met")
	outputDoc := filepath.Join(repo, ".satelle", "tasks", "tsk_out01", "output-"+exe+".md")
	body, err := os.ReadFile(outputDoc)
	if err != nil {
		t.Fatalf("run-output OKF doc not written under the task folder: %v", err)
	}
	for _, want := range []string{"type: task-execution-output", "generated: satelle", "did the work; verification met"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("run-output doc missing %q:\n%s", want, body)
		}
	}

	// Kind-resolved attach root: attaching to the TASK lands under tasks/, not stories/.
	mustRun(t, testBin, repo, "story", "attach", "tsk_out01", "--name", "notes", "--type", "note", "--body", "task-scoped evidence")
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "tasks", "tsk_out01", "notes.md")); err != nil {
		t.Errorf("task attachment did not land under .satelle/tasks/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "stories", "tsk_out01")); !os.IsNotExist(err) {
		t.Error("task attachment wrongly materialised under .satelle/stories/")
	}

	// reindex stays green over the collected output.
	mustRun(t, testBin, repo, "reindex")
}

// TestDispatchedExecutionCapturesOutput drives the real binary to prove the
// DISPATCHED capture path: entering a named-agent run step writes the spawned
// agent's stdout through as the task's OKF run-output doc (sty_890b86cb).
func TestDispatchedExecutionCapturesOutput(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	script := filepath.Join(repo, "runner.sh")
	if err := os.WriteFile(script, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(repo, ".satelle", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[runner]\nharness = \"" + script + " {system}\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-task-workflow.md"), dispatchTaskWorkflow)
	writeAuthoredTask(t, repo, "tsk_disp01")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_disp01", "--title", "dispatched run")
	exe := extractID(out, "exe_")
	if exe == "" {
		t.Fatalf("no execution id in:\n%s", out)
	}

	// Entering in_progress dispatches the runner; its stdout is written through.
	mustRun(t, testBin, repo, "execution", "set", exe, "--status", "in_progress")

	outputDoc := filepath.Join(repo, ".satelle", "tasks", "tsk_disp01", "output-"+exe+".md")
	body, err := os.ReadFile(outputDoc)
	if err != nil {
		t.Fatalf("dispatched run-output doc not written: %v", err)
	}
	if !strings.Contains(string(body), "RUNNER-DID-THE-WORK") {
		t.Errorf("dispatched run's stdout not captured in the output doc:\n%s", body)
	}
}
