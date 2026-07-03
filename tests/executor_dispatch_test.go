//go:build integration

// Black-box coverage for sty_fd427546: a workflow node's agent=<name>
// allocation runs that named agent's .satelle/agents.toml binding at the
// transition — the harness is spawned with the item payload on stdin and the
// system prompt as an argument, a missing binding refuses the transition, and
// the spawned run's evidence lands on the ledger.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgentScript is the stand-in harness binary: it records the system prompt
// (argv) and the stdin payload into the repo, then exits 0.
const fakeAgentScript = `#!/bin/sh
printf '%s' "$1" > .satelle/dispatch-system.txt
cat > .satelle/dispatch-payload.json
echo done
`

// archWorkflow allocates the plan step to the named agent "architect" and
// governs the feature category.
const archWorkflow = `---
name: wf-arch
type: workflow
description: plan step performed by the architect agent
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

// ghostWorkflow allocates a step to an agent no binding defines.
const ghostWorkflow = `---
name: wf-ghost
type: workflow
description: step allocated to an undefined agent
applies_to: ["chore"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  review [agent=ghost]
  done [shape=Msquare]
  backlog -> review -> done
}
` + "```\n"

// TestNamedAgentDispatchRunsBinding: entering plan (agent=architect) spawns the
// [architect] harness from agents.toml — payload on stdin, system prompt as an
// argument — and the transition enacts with the dispatch on the ledger.
func TestNamedAgentDispatchRunsBinding(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	script := filepath.Join(repo, "fake-agent.sh")
	if err := os.WriteFile(script, []byte(fakeAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(repo, ".satelle", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[architect]\nharness = \"" + script + " {system}\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-arch.md"), archWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Align open stories with the architecture", "--body", "Review the backlog for architecture drift", "--acceptance", "1. reviewed")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")

	// The harness ran: it captured the stdin payload (the item) and the system
	// prompt (the executor charter).
	payload, err := os.ReadFile(filepath.Join(repo, ".satelle", "dispatch-payload.json"))
	if err != nil {
		t.Fatalf("fake agent did not run (no payload captured): %v", err)
	}
	if !strings.Contains(string(payload), "Align open stories with the architecture") {
		t.Errorf("payload missing the item:\n%s", payload)
	}
	system, err := os.ReadFile(filepath.Join(repo, ".satelle", "dispatch-system.txt"))
	if err != nil {
		t.Fatalf("fake agent captured no system prompt: %v", err)
	}
	for _, want := range []string{`"architect"`, "Do NOT change the item's status"} {
		if !strings.Contains(string(system), want) {
			t.Errorf("system prompt missing %q:\n%s", want, system)
		}
	}

	// The transition enacted, and the dispatch is ledger evidence.
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "plan"`) {
		t.Errorf("status should be plan:\n%s", got)
	}
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "dispatched step") || !strings.Contains(led, "architect") {
		t.Errorf("ledger missing the dispatch evidence:\n%s", led)
	}
}

// TestNamedAgentMissingBindingRefuses: a workflow step allocated to an agent
// with no agents.toml binding refuses the transition — status unchanged, error
// naming the agent (no silent in-loop fallback).
func TestNamedAgentMissingBindingRefuses(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-ghost.md"), ghostWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Ghost step", "--body", "Prove a missing binding refuses", "--acceptance", "1. refused")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	got, err := run(t, testBin, repo, "story", "set", id, "--status", "review")
	if err == nil {
		t.Fatalf("transition into agent=ghost must refuse without a binding:\n%s", got)
	}
	for _, want := range []string{"ghost", "agents.toml"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal should carry %q:\n%s", want, got)
		}
	}
	after := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(after, `"status": "backlog"`) {
		t.Errorf("status must stay backlog after the refusal:\n%s", after)
	}
}
