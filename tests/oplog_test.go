//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpLogWrittenByBinary drives the REAL satelle binary to prove the flat
// operation log is materialised on disk where a read-only reviewer can scan it
// (sty_be257fef): after a create, a tag reconciliation, and an estimate, the file
// .satelle/logs/operations.log carries the story id and the before/after of each
// mutation — the surface that lets a reviewer verify a DB-state acceptance
// criterion the binary SQLite store hides from it.
func TestOpLogWrittenByBinary(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Log me", "--body", "the goal", "--acceptance", "1. it does X", "--tags", "sprint:9")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in create output:\n%s", out)
	}
	// A tag reconciliation (the case that previously could not be verified at a gate).
	mustRun(t, testBin, repo, "story", "set", id, "--tags", "sprint:9,order:1")
	mustRun(t, testBin, repo, "story", "estimate", id, "--tokens", "1000")

	logPath := filepath.Join(repo, ".satelle", "logs", "operations.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("operation log not written by the binary at %s: %v", logPath, err)
	}
	s := string(b)
	for _, want := range []string{id, "story-create", "story-set", "order:1", "story-estimate"} {
		if !strings.Contains(s, want) {
			t.Errorf("operation log missing %q — a reviewer could not verify the mutation:\n%s", want, s)
		}
	}
}
