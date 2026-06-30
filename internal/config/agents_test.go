package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentsDefault(t *testing.T) {
	ac, err := LoadAgents(t.TempDir()) // no actors.toml present
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != DefaultReviewerTools || got.Harness != DefaultReviewerHarness {
		t.Errorf("reviewer default = %+v, want tools=%q harness=%q", got, DefaultReviewerTools, DefaultReviewerHarness)
	}
	if got := ac.ExecutorBinding(); got.Harness != DefaultExecutorHarness {
		t.Errorf("executor default harness = %q, want %q", got.Harness, DefaultExecutorHarness)
	}
}

// TestLoadAgentsOnlyAgentsToml proves the loader reads agents.toml and that the
// legacy actors.toml is NO LONGER loaded (sty_7db2ed7d): a repo carrying only the
// retired filename resolves to defaults (the zero config), not its bindings.
func TestLoadAgentsOnlyAgentsToml(t *testing.T) {
	// Canonical agents.toml: loads.
	canon := t.TempDir()
	if err := os.WriteFile(filepath.Join(canon, AgentsConfigName), []byte("[reviewer]\nmodel = \"canon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadAgents(canon); err != nil || ac.Reviewer.Model != "canon" {
		t.Fatalf("canonical agents.toml: ac=%+v err=%v", ac, err)
	}

	// Legacy actors.toml only: NOT loaded — resolves to the zero config.
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, ActorsConfigName), []byte("[reviewer]\nmodel = \"legacy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadAgents(legacy); err != nil || ac.Reviewer.Model != "" {
		t.Fatalf("legacy actors.toml must not load: ac=%+v err=%v", ac, err)
	}
}

func TestLoadAgentsOverride(t *testing.T) {
	dir := t.TempDir()
	body := "[reviewer]\ntools = \"Read\"\nharness = \"other-harness\"\n[executor]\nharness = \"claude -p\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != "Read" || got.Harness != "other-harness" {
		t.Errorf("reviewer override = %+v, want tools=Read harness=other-harness", got)
	}
	if got := ac.ExecutorBinding(); got.Harness != "claude -p" {
		t.Errorf("executor override harness = %q, want claude -p", got.Harness)
	}
}
