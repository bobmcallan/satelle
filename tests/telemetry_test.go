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

// writeTelemetryRoute lands a minimal in-loop lifecycle (no gates) so the
// transitions enact quickly and write the status_transition rows the per-step
// report reads.
func writeTelemetryRoute(t *testing.T, repo string) {
	t.Helper()
	writeSpineFixture(t, repo, "", "", "", "in_progress|executor|||", "done||||")
}

func TestStoryLogStepSelfReportAndNoSecrets(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeTelemetryRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Telemetry cost", "--body", "drive in-loop steps", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	// Drive in-loop transitions — these write status_transition rows (per-step
	// wall-time is derived from their timestamps). CombinedOutput captures the
	// step-edge self-report nudge on stderr (sty_56aae77a AC3).
	setOut := mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if !strings.Contains(setOut, "step-self-report") || !strings.Contains(setOut, "step=backlog") {
		t.Errorf("story set --status must nudge step-self-report for the left step:\n%s", setOut)
	}

	// Non-status set must not emit the nudge (title is frozen once engaged).
	prioOut := mustRun(t, testBin, repo, "story", "set", id, "--priority", "low")
	if strings.Contains(prioOut, "step-self-report") {
		t.Errorf("priority-only set must not emit the step-edge nudge:\n%s", prioOut)
	}

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
	// Honesty note (sty_56aae77a): unmeasured is never free; self-report is not measured.
	for _, want := range []string{"unmeasured, never free", "session self-report", "not measured"} {
		if !strings.Contains(cost, want) {
			t.Errorf("cost view honesty note missing %q:\n%s", want, cost)
		}
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

// writeGateTelemetryRoute gates begin-work with a reviewer skill so a stubbed
// no-verdict reviewer drives the dispatch engine's retry/failure path.
func writeGateTelemetryRoute(t *testing.T, repo string) {
	t.Helper()
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor||satelle-story-intent-review|reviewer",
		"done||||")
}

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
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		"[reviewer]\ncommand = \""+stub+" {system}\"\ntools = \"Read\"\n")
	writeGateTelemetryRoute(t, repo)
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

// TestStoryCostUnreportedIsNotZero pins sty_56aae77a end-to-end: a plain-text
// (no usage) reviewer invocation must render as —/— on the gate row itself, not
// a confident 0/0, and the TOTAL must name unreported invocations.
func TestStoryCostUnreportedIsNotZero(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Reviewer stub: emit a valid accept with no usage envelope (plain text).
	stub := filepath.Join(repo, "accept.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"decision\":\"accept\",\"notes\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		"[reviewer]\ncommand = \""+stub+" {system}\"\ntools = \"Read\"\n")
	writeGateTelemetryRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Unreported cost", "--body", "plain-text reviewer", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "5m", "--tokens", "100")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	// Row must exist on the ledger before we trust the cost table.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "agent_invocation") {
		t.Fatalf("expected agent_invocation for the reviewer:\n%s", led)
	}

	cost := mustRun(t, testBin, repo, "story", "cost", id)
	// Gate skill must appear on a row with unreported tokens (not 0/0).
	if !strings.Contains(cost, "satelle-story-intent-review") {
		t.Errorf("cost table missing the gate skill row:\n%s", cost)
	}
	// Find the gate line and require —/— on it (not only in the static note).
	gateLine := ""
	for _, line := range strings.Split(cost, "\n") {
		if strings.Contains(line, "satelle-story-intent-review") {
			gateLine = line
			break
		}
	}
	if gateLine == "" {
		t.Fatalf("no gate row in cost:\n%s", cost)
	}
	if strings.Contains(gateLine, "0/0") {
		t.Errorf("gate row must not render 0/0:\n%s", gateLine)
	}
	if !strings.Contains(gateLine, "—/—") {
		t.Errorf("gate row must render unreported as —/—:\n%s", gateLine)
	}
	// TOTAL names unreported count (measuredTotalLabel), not only the note.
	if !strings.Contains(cost, "invocations unreported") {
		t.Errorf("TOTAL must name unreported invocations:\n%s", cost)
	}
}

// TestStoryCostMeasuredUsageRecorded pins that a Claude-JSON usage envelope
// (including cache fields) is recorded on agent_invocation and surfaces as the
// summed full-prompt input in satelle story cost, with a TOTAL that has no
// unreported clause (sty_8178f1c6 — e2e surface of the cache-sum decision).
func TestStoryCostMeasuredUsageRecorded(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Claude --output-format json envelope: result text + realistic usage with
	// cache fields. Displayed input must be 22+11000+2500 = 13522, not 22.
	stub := filepath.Join(repo, "claude-accept.sh")
	script := `#!/bin/sh
echo '{"result":"{\"decision\":\"accept\",\"notes\":\"ok\"}","usage":{"input_tokens":22,"cache_creation_input_tokens":11000,"cache_read_input_tokens":2500,"output_tokens":59}}'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		"[reviewer]\ncommand = \""+stub+" {system}\"\ntools = \"Read\"\n")
	writeGateTelemetryRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "chore",
		"--title", "Measured cost", "--body", "claude-json usage", "--acceptance", "1. done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "5m", "--tokens", "100")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	cost := mustRun(t, testBin, repo, "story", "cost", id)
	gateLine := ""
	for _, line := range strings.Split(cost, "\n") {
		if strings.Contains(line, "satelle-story-intent-review") {
			gateLine = line
			break
		}
	}
	if gateLine == "" {
		t.Fatalf("no gate row in cost:\n%s", cost)
	}
	// Full prompt in/out (cache folded into input), not the uncached remainder alone.
	if !strings.Contains(gateLine, "13522/59") {
		t.Errorf("gate row must show measured 13522/59 (cache-inclusive input):\n%s", gateLine)
	}
	if !strings.Contains(gateLine, "13581") {
		t.Errorf("gate row must show total 13581:\n%s", gateLine)
	}
	// Measured-only TOTAL: no unreported clause when every invocation reported.
	totalLine := ""
	for _, line := range strings.Split(cost, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "TOTAL") && strings.Contains(line, "13581") {
			totalLine = line
			break
		}
	}
	// At least one TOTAL line should include 13581 without the unreported annotation
	// for the invocations table (first TOTAL).
	foundMeasuredTotal := false
	for _, line := range strings.Split(cost, "\n") {
		if strings.Contains(line, "TOTAL") && strings.Contains(line, "13581") && !strings.Contains(line, "unreported") {
			foundMeasuredTotal = true
			break
		}
	}
	if !foundMeasuredTotal {
		t.Errorf("measured-only TOTAL 13581 without unreported clause not found:\n%s\n(totalLine=%q)", cost, totalLine)
	}
}
