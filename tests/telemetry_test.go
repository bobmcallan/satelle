//go:build integration

// Black-box coverage for sty_b73c3236: the generic `satelle story log` telemetry
// verb (retiring `story step-cost`, sty_3b2e55f5). Driving a story through
// in-loop transitions produces per-step wall-time (derived from the
// status_transition timestamps), `satelle story log --kind step-self-report`
// records a step's self-reported actual tokens + a per-step estimate, `satelle
// story cost` renders the per-step report from the new telemetry_event ledger
// entries, and a logged event's payload is refused when it looks like a secret.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// telemetryWorkflow is a minimal in-loop lifecycle (no gates) so the transitions
// enact quickly and write the status_transition rows the per-step report reads.
const telemetryWorkflow = `---
name: wf-telemetry
type: workflow
description: minimal in-loop lifecycle for telemetry/cost coverage
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

func TestStoryLogStepSelfReportAndNoSecrets(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-telemetry.md"), telemetryWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Telemetry cost", "--body", "drive in-loop steps", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	// Drive in-loop transitions — these write status_transition rows (per-step
	// wall-time is derived from their timestamps).
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// Record the in-loop step's self-reported actual tokens + a per-step estimate
	// via the generic telemetry verb (retiring `story step-cost`).
	mustRun(t, testBin, repo, "story", "log", id, "--kind", "step-self-report",
		"--data", "step=in_progress", "--data", "tokens_total=42000",
		"--data", "est_tokens=50000", "--data", "est_duration_ms=2400000")

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

	// The telemetry_event ledger payload carries numbers + the step name only —
	// never env/secrets (AC4).
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "telemetry_event") || !strings.Contains(led, "42000") {
		t.Errorf("ledger missing the telemetry_event entry:\n%s", led)
	}
	for _, banned := range []string{"ANTHROPIC_", "AUTH_TOKEN", "api_key", "GLM_API_KEY", "z.ai"} {
		if strings.Contains(led, banned) {
			t.Errorf("story-log/ledger leaked a secret-ish token %q:\n%s", banned, led)
		}
	}

	// log requires a kind.
	if _, err := run(t, testBin, repo, "story", "log", id, "--data", "x=1"); err == nil {
		t.Error("log without --kind must be refused")
	}
}

// TestStoryLogRefusesSecretLookingData pins AC4: a data field whose key or value
// looks like a credential is refused outright, never written to the ledger.
func TestStoryLogRefusesSecretLookingData(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Secret refusal", "--body", "b", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	if _, err := run(t, testBin, repo, "story", "log", id, "--kind", "quality-check", "--data", "api_key=sk-abcdef1234567890"); err == nil {
		t.Error("a credential-ish key must be refused")
	}
	if _, err := run(t, testBin, repo, "story", "log", id, "--kind", "quality-check",
		"--data", "note=sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD"); err == nil {
		t.Error("a token-shaped value must be refused")
	}

	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if strings.Contains(led, "telemetry_event") {
		t.Errorf("a refused telemetry event must not reach the ledger:\n%s", led)
	}
}

// gateTelemetryWorkflow gates begin-work with a reviewer skill so a stubbed
// no-verdict reviewer drives the dispatch engine's retry/failure path.
const gateTelemetryWorkflow = `---
name: wf-gate-telemetry
type: workflow
description: gated lifecycle to exercise dispatch-failure telemetry
applies_to: ["chore"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  in_progress -> done
}
` + "```\n"

// TestDispatchTelemetryOnReviewerFailure pins AC2 end-to-end (sty_b73c3236): when
// a gated transition's reviewer never returns a verdict, the dispatch engine —
// via the app-wired telemetry sink — records STRUCTURED agent-retry/agent-failure
// telemetry_event rows on the ledger (not only reviewer.log), so the outcome is
// queryable. This is the coded channel only the binary can observe.
func TestDispatchTelemetryOnReviewerFailure(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A reviewer stub that echoes non-JSON — no parseable verdict, so runReviewer
	// treats every attempt as a transient no-verdict and retries, then fails.
	stub := filepath.Join(repo, "noverdict.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'no verdict here'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"[reviewer]\nharness = \""+stub+" {system}\"\ntools = \"Read\"\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-gate-telemetry.md"), gateTelemetryWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Gated telemetry", "--body", "drive a failing gate", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	// Begin-work is gated; the stub never yields a verdict, so the transition is
	// refused after the retries are exhausted.
	if _, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress"); err == nil {
		t.Fatal("a never-verdict reviewer gate must refuse the transition")
	}

	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	for _, want := range []string{"telemetry_event", "agent-retry", "agent-failure"} {
		if !strings.Contains(led, want) {
			t.Errorf("dispatch engine did not record structured %q telemetry on the ledger:\n%s", want, led)
		}
	}
}
