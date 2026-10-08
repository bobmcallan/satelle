package tests

import (
	"fmt"
	"os"
	"path/filepath"
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

// TestReviewerModelProblem_Resolution covers the pass, empty, missing-profile and
// repo-model cases with a temp repo layer and an in-memory catalog, so it runs in
// CI without the operator's .satelle or ~/.satelle. The check of the operator's
// REAL catalog is TestRepoReviewerModelIsActive (operator_config_test.go, behind
// the operatorconfig tag).
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
