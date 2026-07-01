//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateDeterministicStructure drives the real binary end-to-end to prove
// the structure check is deterministic CODE (sty_a90d5c49): a malformed skill
// fails `satelle validate` with a named problem and no agent CLI involved, and a
// well-formed one passes. No claude -p subprocess, so no flakiness.
func TestValidateDeterministicStructure(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	skillsDir := filepath.Join(repo, ".satelle", "skills")

	// Malformed: no frontmatter at all.
	if err := os.WriteFile(filepath.Join(skillsDir, "bad-skill.md"), []byte("# bad\n\nno frontmatter here"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")

	out, err := run(t, testBin, repo, "validate", "skills", "bad-skill")
	if err == nil {
		t.Fatalf("expected validate to FAIL for a malformed skill, but it passed:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "frontmatter") {
		t.Errorf("validate output should name the missing frontmatter:\n%s", out)
	}

	// Well-formed skill: frontmatter + a rubric body.
	good := "---\nname: good-skill\nkind: skill\ndescription: does a thing\n---\n\n# Good skill\n\nDo the thing carefully."
	if err := os.WriteFile(filepath.Join(skillsDir, "good-skill.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")
	out = mustRun(t, testBin, repo, "validate", "skills", "good-skill")
	if !strings.Contains(out, "PASS") {
		t.Errorf("well-formed skill should PASS validate:\n%s", out)
	}
}
