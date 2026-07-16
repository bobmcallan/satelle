//go:build integration

// Black-box coverage for sty_0aa67b7f: a dispatched named agent's stdout/stderr
// stream INCREMENTALLY to a live per-dispatch log file under
// .satelle/logs/dispatch/ — an operator can tail it while the subprocess runs,
// and whatever streamed before a timeout kill is retained for diagnosis, rather
// than only ever seeing output buffered until exit.
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// streamingWorkflow allocates the plan step to the NAMED agent "architect" so a
// story set transition dispatches a real subprocess whose harness is a fake
// agent script (below).
const streamingWorkflow = `---
name: wf-streaming
type: workflow
description: plan step performed by a streaming fake agent
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan [agent=architect]
  done [shape=Msquare]
  backlog -> plan -> done
}
` + "```\n"

// slowStreamingAgentScript echoes two lines with a real pause between them, then
// exits 0 — a stand-in for an agent whose output arrives over time rather than
// all at once.
const slowStreamingAgentScript = `#!/bin/sh
echo line-one
sleep 0.3
echo line-two
sleep 0.3
echo done
`

// hangingAgentScript echoes one line then sleeps far longer than the binding's
// configured timeout, so the dispatch is expected to be killed mid-run.
const hangingAgentScript = `#!/bin/sh
echo partial-line
exec sleep 5
`

func setupStreamingRepo(t *testing.T, script string, extraAgentsToml string) (repo, storyID string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")

	scriptPath := filepath.Join(repo, "fake-agent.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(repo, ".satelle", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[architect]\ncommand = \"" + scriptPath + " {system}\"\ntools = \"Read,Bash(satelle:*)\"\n" + extraAgentsToml); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-streaming.md"), streamingWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Streamed dispatch story", "--body", "exercise the live sink", "--acceptance", "1. streams")
	storyID = extractID(out, "sty_")
	if storyID == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	return repo, storyID
}

// dispatchLogFile globs for the single per-dispatch live log file under
// .satelle/logs/dispatch/ — the sink DispatchExecutor wires into the runner.
func dispatchLogFile(t *testing.T, repo string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(runtimeRoot(t, repo), "logs", "dispatch", "dispatch-*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one dispatch log file under runtime logs/dispatch, got %v (err %v)", matches, err)
	}
	return matches[0]
}

// TestDispatchStreamsLiveOutputBeforeExit pins AC1/AC3/AC6: the dispatched
// agent's lines land on the live log file WHILE the `story set` transition is
// still running, not only after it returns — proven by starting the CLI command
// in the background and polling the log file for the first line before it has
// written the last one.
func TestDispatchStreamsLiveOutputBeforeExit(t *testing.T) {
	repo, id := setupStreamingRepo(t, slowStreamingAgentScript, "")

	cmd := exec.Command(testBin, "story", "set", id, "--status", "plan")
	cmd.Dir = repo
	cmd.Env = isolatedEnv(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var sawPartial bool
	deadline := time.Now().Add(5 * time.Second)
	logDir := filepath.Join(runtimeRoot(t, repo), "logs", "dispatch")
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(logDir, "dispatch-*.log"))
		if len(matches) == 1 {
			data, _ := os.ReadFile(matches[0])
			if strings.Contains(string(data), "line-one") && !strings.Contains(string(data), "done") {
				sawPartial = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("story set failed: %v", err)
	}
	if !sawPartial {
		t.Error("expected to observe the first streamed line on the live log file before the dispatch finished")
	}

	// AC2: the final structured result still parsed — the transition enacted.
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "plan"`) {
		t.Errorf("status should be plan:\n%s", got)
	}

	// The live log carries every line, including the last one written after exit.
	data, err := os.ReadFile(dispatchLogFile(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"line-one", "line-two", "done"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("dispatch log missing %q:\n%s", want, data)
		}
	}
}

// TestDispatchTimeoutRetainsStreamedOutputOnKill pins AC4: a per-binding timeout
// (sty_446c38b7) shorter than the agent's sleep kills the dispatch, but the line
// it streamed before the kill is retained on the live log file for diagnosis.
func TestDispatchTimeoutRetainsStreamedOutputOnKill(t *testing.T) {
	repo, id := setupStreamingRepo(t, hangingAgentScript, "timeout = \"300ms\"\n")

	out, err := run(t, testBin, repo, "story", "set", id, "--status", "plan")
	if err == nil {
		t.Fatalf("expected the short timeout to kill the hanging agent:\n%s", out)
	}

	data, rerr := os.ReadFile(dispatchLogFile(t, repo))
	if rerr != nil {
		t.Fatalf("dispatch log not written: %v", rerr)
	}
	if !strings.Contains(string(data), "partial-line") {
		t.Errorf("dispatch log should retain the line streamed before the kill: %q", data)
	}
}
