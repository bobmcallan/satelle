//go:build integration

// Black-box coverage for sty_d0d6bb67: `satelle init` validates the deployed
// system (and exits non-zero over broken substrate), and broken configuration
// refuses to run — a malformed or missing agents.toml, or a structurally broken
// governing workflow, produces a clear refusal from the real binary instead of a
// silent fallback to compiled defaults.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitValidatesDeployedSystem: a fresh init ends with the validation pass
// and reports the deployment green.
func TestInitValidatesDeployedSystem(t *testing.T) {
	repo := t.TempDir()
	out := mustRun(t, testBin, repo, "init")
	for _, want := range []string{"Validating the deployed system:", "PASS  deployed system validates green"} {
		if !strings.Contains(out, want) {
			t.Errorf("init report missing %q:\n%s", want, out)
		}
	}
}

// TestInitFailsOnBrokenSubstrate: init over a repo whose authored substrate does
// not validate exits non-zero, naming the failing artifact (AC1).
func TestInitFailsOnBrokenSubstrate(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wfDir, "broken.md"),
		"---\nname: broken\n---\n# broken (no type/description/scope, no DOT)\n")
	out, err := run(t, testBin, repo, "init")
	if err == nil {
		t.Fatalf("init must exit non-zero over broken substrate:\n%s", out)
	}
	if !strings.Contains(out, "FAIL  workflows/broken") {
		t.Errorf("init should name the failing artifact:\n%s", out)
	}
}

// TestMalformedAgentsTomlRefuses: a store-backed command refuses to run when
// agents.toml is unparseable, naming the file (AC2) — no silent fallback.
func TestMalformedAgentsTomlRefuses(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"), "[reviewer\nnot toml\n")
	out, err := run(t, testBin, repo, "status")
	if err == nil {
		t.Fatalf("status must refuse under a malformed agents.toml:\n%s", out)
	}
	if !strings.Contains(out, "agents.toml") {
		t.Errorf("refusal should name agents.toml:\n%s", out)
	}
}

// TestMissingAgentsTomlRefusesAndInitReseeds: an initialized repo whose
// agents.toml is gone refuses store-backed commands with the fix, and re-running
// init reseeds it (AC3) — while a repo with no .satelle at all still bootstraps
// (init is the thing that runs).
func TestMissingAgentsTomlRefusesAndInitReseeds(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	if err := os.Remove(filepath.Join(repo, ".satelle", "agents.toml")); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, testBin, repo, "status")
	if err == nil {
		t.Fatalf("status must refuse when agents.toml is missing in an initialized repo:\n%s", out)
	}
	for _, want := range []string{"agents.toml", "satelle init"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should carry %q:\n%s", want, out)
		}
	}
	// The prescribed fix works: init reseeds, the command runs again.
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "status")
}

// TestBrokenWorkflowRefusesGate: a gated transition under a structurally broken
// governing workflow is refused with the problems (AC4) — the substrate executes
// as defined or refuses.
func TestBrokenWorkflowRefusesGate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// A broken workflow claiming the "feature" category: parseable enough to be
	// selected (applies_to), but failing the structure check (no scope, no DOT).
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-broken.md"),
		"---\nname: wf-broken\ntype: workflow\ndescription: broken on purpose\napplies_to: [\"feature\"]\n---\n# no scope, no DOT\n")
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Gate me", "--body", "Prove the broken workflow refuses", "--acceptance", "1. refused")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	got, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("engagement must be refused under a broken governing workflow:\n%s", got)
	}
	for _, want := range []string{"wf-broken", "structure validation"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal should carry %q:\n%s", want, got)
		}
	}
}
