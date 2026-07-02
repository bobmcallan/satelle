package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestScaffoldAgentsTomlFullyDefined proves the agents layer ships fully
// defined at init (sty_892517e7): the scaffold carries ACTIVE [executor] and
// [reviewer] sections whose values match the coded defaults — no hidden coded
// configuration — and the parsed bindings equal the absent-file defaults.
func TestScaffoldAgentsTomlFullyDefined(t *testing.T) {
	for _, want := range []string{
		"[executor]", `harness = "in-loop"`,
		"[reviewer]", `harness = "claude"`, `tools   = "Read,Grep,Glob"`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold missing active entry %q", want)
		}
	}
	// Parity: loading the scaffold yields the same effective reviewer binding as
	// the coded defaults for an absent file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.AgentsConfigName), []byte(scaffoldAgentsToml), 0o644); err != nil {
		t.Fatal(err)
	}
	ag, err := config.LoadAgents(dir)
	if err != nil {
		t.Fatalf("scaffold does not parse: %v", err)
	}
	rev := ag.ReviewerBinding()
	if rev.Harness != config.DefaultReviewerHarness || rev.Tools != config.DefaultReviewerTools {
		t.Errorf("scaffold reviewer = (%q, %q), want coded defaults (%q, %q)",
			rev.Harness, rev.Tools, config.DefaultReviewerHarness, config.DefaultReviewerTools)
	}
}
