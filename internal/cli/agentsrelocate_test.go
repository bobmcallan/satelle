package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// init is the ONE place that converts an unconverted repo (sty_10f732ed): the
// loader's fallback is a bridge, not a second permanent home, so it must
// actually move the file rather than leave both paths live forever.

func canonicalAgents(repo string) string {
	return filepath.Join(repo, config.DefaultDataDir, config.AgentsConfigDir, config.AgentsConfigName)
}

func legacyAgents(repo string) string {
	return filepath.Join(repo, config.DefaultDataDir, config.AgentsConfigName)
}

func TestInitSeedsAgentsAtCanonicalPath(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canonicalAgents(repo)); err != nil {
		t.Fatalf("fresh init must seed the agents layer at workflows/agents.toml: %v", err)
	}
	if _, err := os.Stat(legacyAgents(repo)); !os.IsNotExist(err) {
		t.Fatal("fresh init must leave nothing at the legacy path")
	}
}

func TestInitRelocatesLegacyAgentsFile(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-move layout: the authored file at the legacy path only.
	body, err := os.ReadFile(canonicalAgents(repo))
	if err != nil {
		t.Fatal(err)
	}
	marker := "\n# authored-by-the-operator\n"
	if err := os.WriteFile(legacyAgents(repo), append(body, []byte(marker)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(canonicalAgents(repo)); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("re-init: %v\n%s", err, out.String())
	}

	moved, err := os.ReadFile(canonicalAgents(repo))
	if err != nil {
		t.Fatalf("the legacy file must be relocated, not copied or dropped: %v", err)
	}
	if !strings.Contains(string(moved), "authored-by-the-operator") {
		t.Fatal("relocation must carry the operator's content, not reseed the scaffold")
	}
	if _, err := os.Stat(legacyAgents(repo)); !os.IsNotExist(err) {
		t.Fatal("the legacy path must be empty after relocation — two live copies is the state this move exists to end")
	}
	if !strings.Contains(out.String(), "relocated") {
		t.Fatalf("init must REPORT the relocation, got:\n%s", out.String())
	}
}

// A repo that has not re-inited still runs: the fallback reads the legacy file
// in place, so the move cannot brick an unconverted repo.
func TestLegacyPathRepoStillResolvesItsAgents(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(canonicalAgents(repo))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyAgents(repo), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(canonicalAgents(repo)); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(repo, config.DefaultDataDir)
	ac, err := config.LoadAgents(dataDir)
	if err != nil {
		t.Fatalf("an unconverted repo must still load its agents layer: %v", err)
	}
	if ac.ReviewerBinding().CommandTemplate() == "" {
		t.Fatal("the legacy file resolved to an empty reviewer binding")
	}
}
