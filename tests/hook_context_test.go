//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookContextInjectsSessionSet drives the real binary to prove SessionStart
// residency is TAG-DRIVEN (epic:session-context): a project principle carrying
// principles:session IS injected, while one WITHOUT the marker is on-demand and is
// NOT. The operating principle satelle-agent-goals ships session-tagged, so it
// rides too.
func TestHookContextInjectsSessionSet(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A session-tagged project principle → must be injected.
	session := "---\nname: my-session-belief\ntype: principle\ntags: [type:principle, principles:session]\n---\n\n# Session belief\n\nThis project belief MUST be auto-injected at session start."
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "principles", "my-session-belief.md"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	// An untagged project principle → on-demand, must NOT be injected.
	demand := "---\nname: my-demand-belief\ntype: principle\ntags: [type:principle]\n---\n\n# On-demand belief\n\nThis project belief stays on demand, never auto-injected."
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "principles", "my-demand-belief.md"), []byte(demand), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")

	out := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(out, "satelle-agent-goals") {
		t.Errorf("the operating principle satelle-agent-goals must be injected:\n%s", out)
	}
	if !strings.Contains(out, "MUST be auto-injected") {
		t.Errorf("a principles:session project principle must be injected:\n%s", out)
	}
	if strings.Contains(out, "my-demand-belief") || strings.Contains(out, "stays on demand") {
		t.Errorf("an on-demand (untagged) principle must NOT be injected:\n%s", out)
	}
}

// TestHookContextNamesSeatMode: the SessionStart inject states the seat
// concurrency mode the session is actually running under (sty_050f3a19 AC3).
// Under the default it stays silent about an unheld seat — that is the whole
// rule, and a line per session would be a token toll for nothing. Under `epic`
// it names the mode and BOTH preconditions before the agent meets them in a
// refusal.
func TestHookContextNamesSeatMode(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Default mode, no seat held: no seat block at all.
	if out := mustRun(t, testBin, repo, "hook", "context"); strings.Contains(out, "seat mode") {
		t.Errorf("default mode with no seat must not inject a seat block:\n%s", out)
	}

	mustRun(t, testBin, repo, "settings", "engagement.parallel", "epic")
	out := mustRun(t, testBin, repo, "hook", "context")
	for _, want := range []string{"seat mode: epic", "parent", "working tree"} {
		if !strings.Contains(out, want) {
			t.Errorf("epic-mode inject missing %q:\n%s", want, out)
		}
	}
}
