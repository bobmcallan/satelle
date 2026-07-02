//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestReindexWarnsUncategorisedStories proves the category conformance rule is
// deterministic code at the index path (sty_af239840): an open story without a
// category draws a reindex WARNING naming --category (no LLM, no gate_create);
// setting the category clears it. Bare creation itself stays legal (stories
// start as stubs; the workflow gates enforce structure progressively).
func TestReindexWarnsUncategorisedStories(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)

	// A bare story (no category) is legal at create…
	out := mustRun(t, testBin, repo, "story", "create", "--title", "Uncategorised stub",
		"--body", "goal", "--acceptance", "1. testable")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id: %s", out)
	}

	// …but reindex reports it, with the actionable fix.
	rout := mustRun(t, testBin, repo, "reindex")
	if !strings.Contains(rout, "without a category") || !strings.Contains(rout, id) {
		t.Errorf("reindex should warn about the uncategorised open story %s:\n%s", id, rout)
	}
	if !strings.Contains(rout, "--category") {
		t.Errorf("the warning must name --category as the fix:\n%s", rout)
	}

	// Fixing the category clears the warning.
	mustRun(t, testBin, repo, "story", "set", id, "--category", "feature")
	rout = mustRun(t, testBin, repo, "reindex")
	if strings.Contains(rout, "without a category") {
		t.Errorf("warning should clear once the category is set:\n%s", rout)
	}
}
