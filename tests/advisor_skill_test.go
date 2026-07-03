//go:build integration

// Black-box coverage for sty_f4c1bd90: the embedded satelle-workflow-advisor
// skill — the semantic workflow review the in-loop agent runs after `satelle
// workflow validate` — seeds at init, validates as substrate, survives a
// rebase, and is discoverable from the workflows help topic.
package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvisorSkillSeedsValidatesAndSurvivesRebase(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	if !fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("init did not seed satelle-workflow-advisor")
	}
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "skill", "validate", "satelle-workflow-advisor")
	if !strings.Contains(out, "PASS  skills/satelle-workflow-advisor") {
		t.Errorf("advisor skill should validate:\n%s", out)
	}

	// A rebase (backup + wipe + redeploy) must redeploy it — it is referenced by
	// no workflow, so the default-solution deploy alone would drop it.
	mustRun(t, testBin, repo, "rebase", "--yes")
	if !fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")) {
		t.Fatal("rebase did not redeploy satelle-workflow-advisor")
	}

	// Discoverable: the workflows help topic points the agent at the review.
	help := mustRun(t, testBin, repo, "help", "workflows")
	if !strings.Contains(help, "satelle-workflow-advisor") {
		t.Errorf("help workflows should reference the advisor skill:\n%s", help)
	}
}
