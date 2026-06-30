package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadActorsDefault(t *testing.T) {
	ac, err := LoadActors(t.TempDir()) // no actors.toml present
	if err != nil {
		t.Fatalf("LoadActors: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != DefaultReviewerTools || got.Harness != DefaultReviewerHarness {
		t.Errorf("reviewer default = %+v, want tools=%q harness=%q", got, DefaultReviewerTools, DefaultReviewerHarness)
	}
	if got := ac.ExecutorBinding(); got.Harness != DefaultExecutorHarness {
		t.Errorf("executor default harness = %q, want %q", got.Harness, DefaultExecutorHarness)
	}
}

// TestLoadActorsAgentsTomlFallback proves the agents.toml/actors.toml back-compat
// (sty_536f9960): the canonical agents.toml loads, the legacy actors.toml still
// loads as a fallback, and agents.toml wins when both are present.
func TestLoadActorsAgentsTomlFallback(t *testing.T) {
	// Legacy actors.toml only: still loads.
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, ActorsConfigName), []byte("[reviewer]\nmodel = \"legacy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadActors(legacy); err != nil || ac.Reviewer.Model != "legacy" {
		t.Fatalf("legacy actors.toml fallback: ac=%+v err=%v", ac, err)
	}

	// Canonical agents.toml: loads.
	canon := t.TempDir()
	if err := os.WriteFile(filepath.Join(canon, AgentsConfigName), []byte("[reviewer]\nmodel = \"canon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadActors(canon); err != nil || ac.Reviewer.Model != "canon" {
		t.Fatalf("canonical agents.toml: ac=%+v err=%v", ac, err)
	}

	// Both present: agents.toml wins.
	both := t.TempDir()
	if err := os.WriteFile(filepath.Join(both, AgentsConfigName), []byte("[reviewer]\nmodel = \"canon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(both, ActorsConfigName), []byte("[reviewer]\nmodel = \"legacy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadActors(both); err != nil || ac.Reviewer.Model != "canon" {
		t.Fatalf("agents.toml should win over actors.toml: ac=%+v err=%v", ac, err)
	}
}

func TestLoadActorsOverride(t *testing.T) {
	dir := t.TempDir()
	body := "[reviewer]\ntools = \"Read\"\nharness = \"other-harness\"\n[executor]\nharness = \"claude -p\"\n"
	if err := os.WriteFile(filepath.Join(dir, ActorsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadActors(dir)
	if err != nil {
		t.Fatalf("LoadActors: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != "Read" || got.Harness != "other-harness" {
		t.Errorf("reviewer override = %+v, want tools=Read harness=other-harness", got)
	}
	if got := ac.ExecutorBinding(); got.Harness != "claude -p" {
		t.Errorf("executor override harness = %q, want claude -p", got.Harness)
	}
}
