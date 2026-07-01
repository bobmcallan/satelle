//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateFileBasedCatchesMalformed proves sty_fbd059d3: validate walks the
// authored FILES, so a malformed skill that never indexes is still caught (FAIL),
// while the reserved keep-file README (no frontmatter) is EXEMPT, not failed.
func TestValidateFileBasedCatchesMalformed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// A malformed skill: no frontmatter — it will not index, but validate must
	// still catch it because it reads the file.
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "bad-skill.md"), "# a heading, no frontmatter\n")

	out, err := run(t, testBin, repo, "skill", "validate")
	if err == nil {
		t.Fatalf("validate should fail on a malformed skill file:\n%s", out)
	}
	if !strings.Contains(out, "bad-skill") || !strings.Contains(out, "FAIL") {
		t.Errorf("the malformed skill should be reported FAIL:\n%s", out)
	}
	if strings.Contains(out, "FAIL  skills/README") {
		t.Errorf("the keep-file README must be exempt, not failed:\n%s", out)
	}
	if !strings.Contains(out, "EXEMPT skills/README") {
		t.Errorf("the keep-file README should be reported EXEMPT:\n%s", out)
	}

	// Removing the malformed file → validate passes clean again.
	if err := os.Remove(filepath.Join(repo, ".satelle", "skills", "bad-skill.md")); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, testBin, repo, "skill", "validate"); err != nil {
		t.Fatalf("validate should pass once the malformed file is gone: %v\n%s", err, out)
	}
}
