//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestWorkflowWithoutDoneGateValidates drives the real binary to prove the spine
// mandate is relaxed (sty_9a139c78): a route whose terminal step carries no
// reviewers still validates, and a transparent always-on gate (the step-summary
// declaration, marked mandatory) parses and validates clean. "If the user breaks
// the process, so be it" — the done gate is the author's choice, not a mandate.
func TestWorkflowWithoutDoneGateValidates(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	writeSpineFixture(t, repo, "", "",
		"## gate satelle-step-summary\nagent: reviewer\nmandatory: true\nfor: *\n",
		"in_progress|executor|||",
		"done||||")
	mustRun(t, testBin, repo, "reindex", "--validate=false")

	out := mustRun(t, testBin, repo, "workflow", "validate")
	if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("a route without a done gate + a transparent always-on gate should validate clean:\n%s", out)
	}
}
