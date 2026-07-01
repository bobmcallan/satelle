//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInitMaterializesBaselineGateSkills proves a fresh `satelle init` lays down
// WORKING default gates (sty_5b8bd8b2): the embedded baseline workflow references
// satelle-story-{intent,done,cancel}-review, and init must materialise all of them
// (plus satelle-step-summary) into .satelle/skills so the gates resolve and enforce
// rather than silently degrading to advisory. A repo that already authored a
// workflow is untouched, so use a clean temp repo.
func TestInitMaterializesBaselineGateSkills(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	for _, name := range []string{
		"satelle-story-intent-review",
		"satelle-story-done-review",
		"satelle-story-cancel-review",
		"satelle-step-summary",
	} {
		p := filepath.Join(repo, ".satelle", "skills", name+".md")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("init did not materialise the baseline gate skill %s: %v", name, err)
		}
	}

	// The materialised substrate validates clean (the embedded gate skills pass the
	// deterministic skill structure check).
	mustRun(t, testBin, repo, "reindex")
	if out, err := run(t, testBin, repo, "skill", "validate"); err != nil {
		t.Fatalf("validate skills should pass on a fresh init:\n%s\n%v", out, err)
	}
}
