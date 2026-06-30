//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReviewerModelActorsBoots drives the REAL binary with this repo's activated
// actors.toml (reviewer model = "sonnet", sty_5073df2f) installed into an isolated
// temp repo: the binary must boot, index, and report status cleanly with the
// reviewer-model binding active. It is the integration counterpart to the unit
// TestRepoReviewerModelIsSonnet — proving the activated config loads end-to-end
// through the binary (applyActorGrants resolves the binding on store open) and
// does not regress a fresh repo. The artifact under test is the repo's real
// actors.toml, so a malformed/regressed activation is caught here too.
func TestReviewerModelActorsBoots(t *testing.T) {
	bin := testBin
	repo := t.TempDir()

	mustRun(t, bin, repo, "init")

	// Overwrite the scaffold agents.toml with this repo's real, activated binding
	// (read from the repo's own agents.toml). Writing the canonical agents.toml
	// ensures it is the binding the loader resolves.
	src := filepath.Join(repoRootForTest(), ".satelle", "agents.toml")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read agents source %s: %v", src, err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	// The binary opens the store (applyAgentGrants resolves the [reviewer] binding,
	// SetReviewerModel("sonnet")) on every command — these must succeed with the
	// activated config present.
	mustRun(t, bin, repo, "index")
	mustRun(t, bin, repo, "status")
}
