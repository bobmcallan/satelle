//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestHelpRouting drives the real binary to prove `satelle help` routing
// (sty_6fcb651d): a process topic prints its body, a command name routes to that
// command's help, and an unknown arg errors with guidance.
func TestHelpRouting(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Process topic.
	out := mustRun(t, testBin, repo, "help", "substrate")
	if !strings.Contains(out, "Open Knowledge Format") {
		t.Errorf("help substrate should print the topic:\n%s", out)
	}

	// Command name → command help (not a flat error).
	out = mustRun(t, testBin, repo, "help", "story")
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "story") {
		t.Errorf("help <command> should render command help:\n%s", out)
	}

	// Unknown arg → error.
	if out, err := run(t, testBin, repo, "help", "definitely-not-a-thing"); err == nil {
		t.Errorf("help <unknown> should error:\n%s", out)
	}
}
