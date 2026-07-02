//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubReviewerProse wires a reviewer stub that answers in PROSE (no JSON
// decision object) — the shape that used to be misread as a transient
// no-verdict (sty_9485d47e).
func stubReviewerProse(t *testing.T, repo, prose string) {
	t.Helper()
	stub := filepath.Join(repo, "verdict-prose.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '"+prose+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"),
		[]byte(fmt.Sprintf("[reviewer]\nharness = \"%s {system} {tools} {model}\"\n", stub)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProseVerdictGatesTransition proves a reviewer that concludes in prose
// still gates deterministically (sty_9485d47e): a prose REJECT blocks the
// transition with the reviewer's reasons in the surfaced error (no blind
// retries, no "transient agent failure" misdirection), and a prose ACCEPT
// advances it.
func TestProseVerdictGatesTransition(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Create needs a verdict too — start with prose ACCEPT so create passes.
	stubReviewerProse(t, repo, "Verdict: accept. Well-formed.")
	out := mustRun(t, testBin, repo, "story", "create", "--title", "Prose-gated story",
		"--body", "goal", "--acceptance", "1. testable")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id: %s", out)
	}

	// Prose REJECT blocks, surfacing the reviewer's reasons.
	stubReviewerProse(t, repo, "Verdict: **reject**. Missing a plan estimate — record one first.")
	rejOut, err := run(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	if err == nil {
		t.Fatalf("prose reject should block the transition:\n%s", rejOut)
	}
	if !strings.Contains(rejOut, "Missing a plan estimate") {
		t.Errorf("the reviewer's prose reasons must reach the executor:\n%s", rejOut)
	}
	if strings.Contains(rejOut, "transient agent failure") {
		t.Errorf("a prose verdict must not be misread as a transient failure:\n%s", rejOut)
	}

	// Prose ACCEPT advances.
	stubReviewerProse(t, repo, "All good. Verdict: accept.")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Errorf("prose accept should advance the story:\n%s", got)
	}
}
