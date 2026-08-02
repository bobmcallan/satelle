//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repo agents layer moved to .satelle/workflows/agents.toml (sty_10f732ed).
// AC5 asks for the proof that matters operationally: BOTH layouts validate green
// AND actually dispatch a gate under the binding they declare. Resolution alone
// is not the claim — a path change that resolved but never dispatched would pass
// a unit test and brick a real repo.

// stubAgentsAt writes the reviewer stub and its binding to an explicit path, so
// the same fixture can be planted at either the canonical or the legacy location.
// Returns the gate log path.
func stubAgentsAt(t *testing.T, repo, agentsPath string) string {
	t.Helper()
	logPath := filepath.Join(repo, "relocate-gate.log")
	stub := filepath.Join(repo, "verdict-relocate.sh")
	script := `#!/bin/sh
sys="$1"
name=$(printf '%s\n' "$sys" | sed -n 's/^name:[[:space:]]*//p' | head -1)
echo "$name" >> "` + logPath + `"
echo "{\"decision\":\"accept\",\"notes\":\"ok\"}"
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// model= is the observable that proves the DECLARED binding drove the
	// dispatch: agent validate echoes it back as effective_model.
	body := fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\nmodel = \"relocate-probe\"\n", stub)
	if err := os.MkdirAll(filepath.Dir(agentsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentsPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return logPath
}

// driveOneGate creates a story and advances it one step, which fires the intent
// gate through whichever binding resolved. Returns the gate log contents.
func driveOneGate(t *testing.T, repo, logPath, title string) string {
	t.Helper()
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", title,
		"--body", "Prove the agents layer dispatches from its resolved location.",
		"--acceptance", "1. the intent gate runs under the declared binding",
		"--category", "chore")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id: %s", out)
	}
	mustRun(t, testBin, repo, "story", "estimate", id, "--time", "10m", "--tokens", "1000")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Fatalf("the gate must have accepted and advanced the story:\n%s", got)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the gate never dispatched — no log at %s: %v", logPath, err)
	}
	return string(body)
}

// AC5, layout 1: a fresh init lands the file at the canonical path, validates
// green, and dispatches its gate from there.
func TestFreshInitAgentsLayerDispatches(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	canonical := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("fresh init must seed the agents layer at workflows/agents.toml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "agents.toml")); !os.IsNotExist(err) {
		t.Fatal("fresh init must leave nothing at the legacy path")
	}

	logPath := stubAgentsAt(t, repo, canonical)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, `effective_model="relocate-probe"`) {
		t.Fatalf("validate must resolve the canonical file's binding:\n%s", out)
	}
	if log := driveOneGate(t, repo, logPath, "Canonical layout gate"); !strings.Contains(log, "satelle-story-intent-review") {
		t.Fatalf("the intent gate did not dispatch under the declared binding: %q", log)
	}
}

// AC5, layout 2: an UNCONVERTED repo — the file at the legacy path only, no init
// re-run — still validates green and still dispatches. This is the leg that
// proves the move cannot brick a repo that has not yet converted.
func TestLegacyLayoutAgentsLayerStillDispatches(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	canonical := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	legacy := filepath.Join(repo, ".satelle", "agents.toml")
	logPath := stubAgentsAt(t, repo, legacy)
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, `effective_model="relocate-probe"`) {
		t.Fatalf("an unconverted repo must still resolve its legacy binding:\n%s", out)
	}
	if log := driveOneGate(t, repo, logPath, "Legacy layout gate"); !strings.Contains(log, "satelle-story-intent-review") {
		t.Fatalf("the intent gate did not dispatch from the legacy path: %q", log)
	}

	// And converting it is a re-init away: the file moves, keeps its content, and
	// the repo stays green.
	relocOut := mustRun(t, testBin, repo, "init")
	if !strings.Contains(relocOut, "relocated") {
		t.Fatalf("init must report the relocation:\n%s", relocOut)
	}
	moved, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("init must relocate the legacy file: %v", err)
	}
	if !strings.Contains(string(moved), "relocate-probe") {
		t.Fatal("relocation must carry the operator's content, not reseed the scaffold")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("the legacy path must be empty after relocation")
	}
	out = mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, `effective_model="relocate-probe"`) {
		t.Fatalf("the relocated repo must still validate green:\n%s", out)
	}
}
