//go:build integration

// Black-box coverage for sty_f5bd176f: a repo can opt its in_progress step into a
// dispatched code-writing `coder` agent (agent=coder). Two invariants are proven
// end-to-end WITHOUT a running serve (so the engaged-story edit gate cannot ride
// the serve fail-open):
//   - dispatched from a PERFORMING state (plan), the coder's edit to a product .go
//     file is ALLOWED by the edit gate during dispatch — the story sits in `plan`,
//     a performing/engaged state, so `satelle hook gate` exits 0 legitimately;
//   - dispatched from backlog under the engagement lease (sty_8426b9c0): the
//     lease is claimed at-start so the edit gate allows during dispatch without
//     the old FROM-performing band-aid.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coderPerformingWorkflow reaches the dispatched coder from `plan`, an in-loop
// PERFORMING node, so the story is engaged while the coder edits. The
// plan->in_progress edge is ungated to isolate the dispatch from the heavy gates.
const coderPerformingWorkflow = `---
name: wf-coder-ok
type: workflow
description: in_progress dispatched to a coder, reached from the performing plan state
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan [agent=executor]
  in_progress [agent=coder, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> done
}
` + "```\n"

// coderFromBacklogWorkflow reaches the coder directly from backlog. Under the
// engagement lease (sty_8426b9c0) this proceeds — the prior FROM-performing
// refusal is gone (sty_f5bd176f band-aid removed).
const coderFromBacklogWorkflow = `---
name: wf-coder-bad
type: workflow
description: coder wired from backlog; lease authorises edit gate during dispatch
applies_to: ["chore"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=coder, prompt="@skill:code"]
  done [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```\n"

// seedCodeSkill authors a minimal repo-local code skill. The binary no longer
// ships an embedded code.md (sty_01f49dd5); workflows that reference @skill:code
// must resolve the rubric from disk.
func seedCodeSkill(t *testing.T, repo string) {
	t.Helper()
	dir := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: code\nscope: project\ntype: skill\ntags: [type:skill]\n" +
		"description: Test fixture executor rubric for coder dispatch.\n---\n\n# code\n\nImplement the slice.\n"
	if err := os.WriteFile(filepath.Join(dir, "code.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendCoderBinding wires a [coder] binding whose harness is the stub script,
// with a CODE-WRITING grant (Write/Edit) plus the mandatory read-only satelle CLI.
func appendCoderBinding(t *testing.T, repo, script string) {
	t.Helper()
	appendCoderBindingTools(t, repo, script, "Read,Edit,Write,Bash(satelle:*)")
}

// appendCoderBindingTools is the same as appendCoderBinding with an explicit tools grant.
func appendCoderBindingTools(t *testing.T, repo, script, tools string) {
	t.Helper()
	agents := filepath.Join(repo, ".satelle", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[coder]\ncommand = \"" + script + " {system}\"\ntools = \"" + tools + "\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// TestCoderDispatchEditGateAllowsFromPerformingState: entering in_progress
// (agent=coder) from the performing `plan` state dispatches the stub coder; during
// the dispatch the coder runs `satelle hook gate` against a product .go path and it
// EXITS 0 — the story is legitimately engaged (status still `plan`, a performing
// state) with NO serve running, so the allow is real, not the fail-open. The
// transition then enacts and the dispatch is on the ledger.
func TestCoderDispatchEditGateAllowsFromPerformingState(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedCodeSkill(t, repo)

	// The stub coder: consume the dispatch payload, then prove the edit gate allows
	// a .go edit DURING dispatch (testBin is the binary under test — the same CLI
	// the coder's Bash(satelle:*) grant would invoke), recording the gate exit code.
	script := filepath.Join(repo, "stub-coder.sh")
	body := "#!/bin/sh\n" +
		"cat > .satelle/coder-payload.json\n" +
		"printf '{\"tool_input\":{\"file_path\":\"%s/internal/coder_edit.go\"}}' \"$PWD\" | " + testBin + " hook gate\n" +
		"echo \"gate_exit=$?\" > .satelle/coder-gate-exit.txt\n" +
		"echo done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	appendCoderBinding(t, repo, script)

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-coder-ok.md"), coderPerformingWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Build the slice", "--body", "coder builds the plan's slice", "--acceptance", "1. built")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	// plan is an in-loop performing node (no dispatch); status becomes plan.
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	// in_progress=coder: dispatch fires while status is still `plan`.
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// The stub coder ran.
	if _, err := os.ReadFile(filepath.Join(repo, ".satelle", "coder-payload.json")); err != nil {
		t.Fatalf("coder did not run (no payload captured): %v", err)
	}
	// The CORE proof: the edit gate ALLOWED the coder's .go edit during dispatch.
	gate, err := os.ReadFile(filepath.Join(repo, ".satelle", "coder-gate-exit.txt"))
	if err != nil {
		t.Fatalf("coder did not record the gate exit: %v", err)
	}
	if !strings.Contains(string(gate), "gate_exit=0") {
		t.Errorf("edit gate must ALLOW the coder's edit during dispatch (via legitimate engaged status, no serve), got: %s", gate)
	}

	// The transition enacted, and the dispatch is ledger evidence.
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Errorf("status should be in_progress:\n%s", got)
	}
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "dispatched step") || !strings.Contains(led, "coder") {
		t.Errorf("ledger missing the coder dispatch evidence:\n%s", led)
	}
}

// TestCoderDispatchFromBacklogUnderLease: with the engagement lease
// (sty_8426b9c0) acquired at-start for the TARGET engaging state, a code-writer
// may dispatch from backlog. The prior FROM-performing band-aid is removed; the
// lease (not FROM status) authorises the edit gate during dispatch.
func TestCoderDispatchFromBacklogUnderLease(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedCodeSkill(t, repo)

	script := filepath.Join(repo, "stub-coder.sh")
	body := "#!/bin/sh\n" +
		"cat > .satelle/coder-ran.json\n" +
		"printf '{\"tool_input\":{\"file_path\":\"%s/internal/coder_edit.go\"}}' \"$PWD\" | " + testBin + " hook gate\n" +
		"echo \"gate_exit=$?\" > .satelle/coder-gate-exit.txt\n" +
		"echo done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	appendCoderBinding(t, repo, script)

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-coder-bad.md"), coderFromBacklogWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Coder from backlog under lease", "--body", "lease authorises edit gate", "--acceptance", "1. coded")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	got, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err != nil {
		t.Fatalf("coder from backlog must proceed under lease model:\n%s\nerr=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "coder-ran.json")); err != nil {
		t.Fatalf("coder must run: %v", err)
	}
	gate, err := os.ReadFile(filepath.Join(repo, ".satelle", "coder-gate-exit.txt"))
	if err != nil {
		t.Fatalf("gate exit not recorded: %v", err)
	}
	if !strings.Contains(string(gate), "gate_exit=0") {
		t.Errorf("edit gate must ALLOW during dispatch via lease (status still backlog): %s", gate)
	}
}

// TestCoderDispatchAcceptsGrokReadFileChannel: a write-capable grok-shaped tools
// grant with read_file (disk context channel, sty_565a0202) dispatches; a
// channel-less write grant is refused with an actionable error naming the fix.
func TestCoderDispatchAcceptsGrokReadFileChannel(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedCodeSkill(t, repo)

	script := filepath.Join(repo, "stub-coder.sh")
	body := "#!/bin/sh\n" +
		"cat > .satelle/coder-readfile-payload.json\n" +
		"echo PLAN-CONSUMED: plan — steps: wire binding, flip workflow\n" +
		"echo done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// Grok-shaped tools: disk channel via read_file, no Bash(satelle:*).
	appendCoderBindingTools(t, repo, script, "read_file,grep,list_dir,write,search_replace")

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-coder-ok.md"), coderPerformingWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Coder disk channel", "--body", "read_file is a context channel", "--acceptance", "1. dispatched")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	if _, err := os.ReadFile(filepath.Join(repo, ".satelle", "coder-readfile-payload.json")); err != nil {
		t.Fatalf("coder with read_file grant must run: %v", err)
	}
	// executor.log captures the PLAN-CONSUMED evidence line.
	logPath := filepath.Join(runtimeRoot(t, repo), "logs", "executor.log")
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("executor.log missing after dispatch: %v", err)
	}
	if !strings.Contains(string(logBody), "PLAN-CONSUMED:") {
		t.Errorf("executor.log should capture plan-consumed stdout:\n%s", logBody)
	}

	// Channel-less write grant refuses before run.
	repo2 := t.TempDir()
	mustRun(t, testBin, repo2, "init")
	seedCodeSkill(t, repo2)
	script2 := filepath.Join(repo2, "stub-coder.sh")
	if err := os.WriteFile(script2, []byte("#!/bin/sh\necho should-not-run\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	appendCoderBindingTools(t, repo2, script2, "write,search_replace,grep,list_dir")
	writeFile(t, filepath.Join(repo2, ".satelle", "workflows", "wf-coder-ok.md"), coderPerformingWorkflow)
	mustRun(t, testBin, repo2, "reindex")
	out2 := mustRun(t, testBin, repo2, "story", "create", "--category", "feature",
		"--title", "Channel-less coder", "--body", "must refuse", "--acceptance", "1. refused")
	id2 := extractID(out2, "sty_")
	mustRun(t, testBin, repo2, "story", "set", id2, "--status", "plan")
	got, err := run(t, testBin, repo2, "story", "set", id2, "--status", "in_progress")
	if err == nil {
		t.Fatalf("channel-less grant must refuse, got ok:\n%s", got)
	}
	if !strings.Contains(got, "context channel") {
		t.Errorf("refusal should name context channel, got:\n%s", got)
	}
}
