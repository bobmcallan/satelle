//go:build integration && operatorconfig

package tests

import (
	"os"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestRepoReviewerModelIsActive pins this repo's dogfood substrate when present:
// the reviewer's EFFECTIVE model — resolved through the machine-wide profile
// catalog (sty_6388f140 moved execution detail there) — stays a non-empty value,
// so the reviewer runs on a pinned model rather than silently falling back to the
// CLI default. The SPECIFIC model is an operator choice, so the test asserts the
// knob is set, not a particular value. The wiring from binding → reviewer
// subprocess is covered by internal/agentstep.TestReviewerModelReachesRunner.
//
// It checks the OPERATOR's real machine config, so it runs only under the
// operatorconfig opt-in (make operator-check), never in the hermetic suites; the
// in-memory resolution is TestReviewerModelProblem_Resolution.
//
// After sty_91a390a0, .satelle/ is gitignored (operator-owned). CI clones have no
// agents.toml; skip when the file is absent so the pin remains a local dogfood
// check, not a false CI red.
func TestRepoReviewerModelIsActive(t *testing.T) {
	dataDir := repoProcessDataDir(t)
	if _, err := os.Stat(func() string { p, _ := config.AgentsPath(dataDir); return p }()); os.IsNotExist(err) {
		t.Skip(".satelle/agents.toml absent (gitignored operator substrate); dogfood pin is local-only")
	}
	repo, err := config.LoadAgents(dataDir)
	if err != nil {
		t.Fatalf("load %s/agents.toml: %v", dataDir, err)
	}
	// This dogfood pin deliberately reads the OPERATOR's real machine catalog
	// (read-only) — the one this repo's profile= references resolve against.
	// Always point SATELLE_HOME at it: GlobalDir panics under go test without
	// SATELLE_HOME (sty_c36c211f), and the integration suite's TestMain isolates
	// SATELLE_HOME to an empty temp home, where the profiles do not exist.
	if hostRootsAtStart.SatelleHome == "" {
		t.Skip("no host SATELLE_HOME was resolvable at suite start")
	}
	t.Setenv("SATELLE_HOME", hostRootsAtStart.SatelleHome)
	catalog, err := config.LoadGlobalAgents()
	if err != nil {
		t.Fatalf("load machine agents catalog: %v", err)
	}
	if msg := reviewerModelProblem(repo, catalog); msg != "" {
		t.Error(msg)
	}
}
