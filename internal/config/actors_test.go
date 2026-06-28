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
