//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReviewerProfileBoots drives the REAL binary with a repo agents.toml whose
// reviewer names a machine-wide profile (profile = "…", sty_6388f140) and a
// fixture catalog in the test's isolated SATELLE_HOME: the binary must boot,
// index and report status cleanly. It is hermetic — the fixture catalog is a
// literal; the check against the operator's REAL catalog is
// TestOperatorCatalogReviewerBoots (operatorconfig tag).
func TestReviewerProfileBoots(t *testing.T) {
	bin := testBin
	repo := t.TempDir()

	mustRun(t, bin, repo, "init")

	agents := "[reviewer]\nprofile = \"fixture-reviewer\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "workflows", "agents.toml"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := "[profiles.fixture-reviewer]\ncommand = \"claude -p\"\nmodel = \"fixture-model\"\n"
	if err := os.WriteFile(filepath.Join(isolatedHome(t), "agents.toml"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, bin, repo, "reindex")
	mustRun(t, bin, repo, "status")
}
