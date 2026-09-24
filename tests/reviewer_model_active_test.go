package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// reviewerModelProblem resolves the reviewer the way satelle itself does
// (repo layer over the machine-wide profile catalog) and returns "" when the
// EFFECTIVE model is pinned, else a diagnostic. A profile the catalog lacks is
// reported by name, not as an empty model.
func reviewerModelProblem(repo config.AgentsConfig, catalog config.GlobalAgentsConfig) string {
	resolved, _, err := config.ResolveAgents(repo, catalog)
	if err != nil {
		return fmt.Sprintf("reviewer profile %q did not resolve against the machine catalog: %v", repo.Reviewer.Profile, err)
	}
	if resolved.ReviewerBinding().Model == "" {
		return fmt.Sprintf("reviewer model is empty — the repo agents.toml or its profile %q must keep the reviewer-model knob active (a pinned model)", repo.Reviewer.Profile)
	}
	return ""
}

// TestRepoReviewerModelIsActive pins this repo's dogfood substrate when present:
// the reviewer's EFFECTIVE model — resolved through the machine-wide profile
// catalog (sty_6388f140 moved execution detail there) — stays a non-empty value,
// so the reviewer runs on a pinned model rather than silently falling back to the
// CLI default. The SPECIFIC model is an operator choice, so the test asserts the
// knob is set, not a particular value. The wiring from binding → reviewer
// subprocess is covered by internal/agentstep.TestReviewerModelReachesRunner.
//
// After sty_91a390a0, .satelle/ is gitignored (operator-owned). CI clones have no
// agents.toml; skip when the file is absent so the pin remains a local dogfood
// check, not a false CI red.
func TestRepoReviewerModelIsActive(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dataDir := filepath.Join(filepath.Dir(filepath.Dir(file)), ".satelle")
	if _, err := os.Stat(func() string { p, _ := config.AgentsPath(dataDir); return p }()); os.IsNotExist(err) {
		t.Skip(".satelle/agents.toml not present (gitignored operator substrate); dogfood pin is local-only")
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
	home, herr := os.UserHomeDir()
	if herr != nil {
		t.Skipf("no home directory to locate the machine catalog: %v", herr)
	}
	t.Setenv("SATELLE_HOME", filepath.Join(home, ".satelle"))
	catalog, err := config.LoadGlobalAgents()
	if err != nil {
		t.Fatalf("load machine agents catalog: %v", err)
	}
	if msg := reviewerModelProblem(repo, catalog); msg != "" {
		t.Error(msg)
	}
}

// TestReviewerModelProblem_Resolution covers the pass, empty, missing-profile and
// repo-model cases with a temp repo layer and an in-memory catalog, so it runs in
// CI without the operator's .satelle or ~/.satelle.
func TestReviewerModelProblem_Resolution(t *testing.T) {
	profiles := func(m map[string]config.AgentBinding) config.GlobalAgentsConfig {
		return config.GlobalAgentsConfig{Profiles: m}
	}
	cases := []struct {
		name    string
		toml    string
		catalog config.GlobalAgentsConfig
		want    string // substring of the problem; "" means no problem
		notWant string
	}{
		{"profile model", "[reviewer]\nprofile = \"p\"\n", profiles(map[string]config.AgentBinding{"p": {Model: "m"}}), "", ""},
		{"repo model wins", "[reviewer]\nmodel = \"m\"\n", config.GlobalAgentsConfig{}, "", ""},
		{"empty model", "[reviewer]\nprofile = \"p\"\n", profiles(map[string]config.AgentBinding{"p": {}}), "model is empty", ""},
		{"missing profile", "[reviewer]\nprofile = \"nope\"\n", config.GlobalAgentsConfig{}, "nope", "model is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path, _ := config.AgentsPath(dir)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			repo, err := config.LoadAgents(dir)
			if err != nil {
				t.Fatalf("load repo layer: %v", err)
			}
			got := reviewerModelProblem(repo, c.catalog)
			if c.want == "" && got != "" {
				t.Fatalf("want no problem, got %q", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("problem %q does not contain %q", got, c.want)
			}
			if c.notWant != "" && strings.Contains(got, c.notWant) {
				t.Fatalf("problem %q must not contain %q", got, c.notWant)
			}
		})
	}
}
