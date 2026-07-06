//go:build integration

// Black-box coverage for sty_3b2e55f5: per-step est-vs-actual cost. Driving a story
// through in-loop transitions produces per-step wall-time (derived from the
// status_transition timestamps), `satelle story step-cost` records a step's
// self-reported actual tokens + a per-step estimate, `satelle story cost` renders
// the per-step report, and the step_cost ledger payload carries numbers only — no
// env/secrets.
package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// stepCostWorkflow is a minimal in-loop lifecycle (no gates) so the transitions
// enact quickly and write the status_transition rows the per-step report reads.
const stepCostWorkflow = `---
name: wf-stepcost
type: workflow
description: minimal in-loop lifecycle for per-step cost coverage
applies_to: ["chore"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```\n"

func TestStoryStepCostReportAndNoSecrets(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-stepcost.md"), stepCostWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Per-step cost", "--body", "drive in-loop steps", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	// Drive in-loop transitions — these write status_transition rows (per-step
	// wall-time is derived from their timestamps).
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// Record the in-loop step's self-reported actual tokens + a per-step estimate.
	mustRun(t, testBin, repo, "story", "step-cost", id,
		"--step", "in_progress", "--tokens", "42000", "--est-tokens", "50000", "--est-time", "40m")

	cost := mustRun(t, testBin, repo, "story", "cost", id)
	// The per-step report is present with the in-loop step row and the merged
	// actual tokens + estimate columns.
	for _, want := range []string{"STEP", "WALL-TIME", "ACTUAL TOKENS", "EST TOKENS", "in_progress", "42000", "50000", "40m"} {
		if !strings.Contains(cost, want) {
			t.Errorf("story cost missing %q:\n%s", want, cost)
		}
	}
	// The honesty note: an unmeasured in-loop token cell is not "free".
	if !strings.Contains(cost, "not free") {
		t.Errorf("cost view should explain that '—' means unmeasured, not free:\n%s", cost)
	}

	// The step_cost ledger payload carries numbers + the step name only — never
	// env/secrets (AC4).
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "step_cost") || !strings.Contains(led, "42000") {
		t.Errorf("ledger missing the step_cost entry:\n%s", led)
	}
	for _, banned := range []string{"ANTHROPIC_", "AUTH_TOKEN", "api_key", "GLM_API_KEY", "z.ai"} {
		if strings.Contains(led, banned) {
			t.Errorf("step_cost/ledger leaked a secret-ish token %q:\n%s", banned, led)
		}
	}

	// step-cost requires a step name.
	if _, err := run(t, testBin, repo, "story", "step-cost", id, "--tokens", "1"); err == nil {
		t.Error("step-cost without --step must be refused")
	}
}
