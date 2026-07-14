//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSurfaceVocabulary (sty_034d843c): controlled [tags.vocabulary] rejects
// unknown values at create, accepts multi-surface via repeated keys, list
// --tag ANY-matches, and leaves no-surface stories valid. No matcher change.
func TestSurfaceVocabulary(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Declare this repo's surface vocabulary (the only place values live).
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n[tags.vocabulary]\nsurface = [\"ui\", \"cli\"]\n"
	if err := os.WriteFile(cfgPath, append(cfg, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	// Disable LLM create gate so structure-only path exercises the vocab check.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = false\n")

	// AC1: unknown surface value rejected with named error listing allowed values.
	out, err := run(t, testBin, repo, "story", "create",
		"--title", "Bad surface",
		"--body", "Should fail",
		"--acceptance", "1. n/a",
		"--category", "feature",
		"--tags", "surface:web")
	if err == nil {
		t.Fatalf("surface:web should be rejected; out:\n%s", out)
	}
	for _, want := range []string{"surface", "web", "ui", "cli"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject error should name %q:\n%s", want, out)
		}
	}

	// AC4: multi-surface via repeated keys; --tag surface:ui ANY-matches.
	// CLI --tags is StringSlice: surface:ui,surface:cli becomes two tags.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Dual surface story",
		"--body", "Touches both interfaces",
		"--acceptance", "1. both surfaces tagged",
		"--category", "feature",
		"--tags", "surface:ui,surface:cli")
	var dual struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &dual); err != nil {
		t.Fatalf("parse dual create: %v\n%s", err, out)
	}
	hasUI, hasCLI := false, false
	for _, tg := range dual.Tags {
		if tg == "surface:ui" {
			hasUI = true
		}
		if tg == "surface:cli" {
			hasCLI = true
		}
	}
	if !hasUI || !hasCLI {
		t.Fatalf("want surface:ui and surface:cli, got %v", dual.Tags)
	}

	list := mustRun(t, testBin, repo, "story", "list", "--tag", "surface:ui")
	if !strings.Contains(list, dual.ID) && !strings.Contains(list, "Dual surface") {
		t.Errorf("--tag surface:ui should match dual-surface story:\n%s", list)
	}

	// AC6: case-insensitive accept, stored canonical.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Cased surface",
		"--body", "UI casing probe",
		"--acceptance", "1. canonical stored",
		"--category", "feature",
		"--tags", "surface:UI")
	var cased struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &cased); err != nil {
		t.Fatalf("parse cased: %v\n%s", err, out)
	}
	foundCanon := false
	for _, tg := range cased.Tags {
		if tg == "surface:ui" {
			foundCanon = true
		}
		if tg == "surface:UI" {
			t.Errorf("stored non-canonical %q", tg)
		}
	}
	if !foundCanon {
		t.Fatalf("want surface:ui stored, got %v", cased.Tags)
	}

	// AC7: no surface: tag stays valid; free-form area: untouched.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "No surface",
		"--body", "Plain leaf work",
		"--acceptance", "1. done",
		"--category", "feature",
		"--tags", "area:web")
	var plain struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &plain); err != nil {
		t.Fatalf("parse plain: %v\n%s", err, out)
	}
	hasArea := false
	for _, tg := range plain.Tags {
		if tg == "area:web" {
			hasArea = true
		}
	}
	if !hasArea {
		t.Fatalf("area:web should pass through, got %v", plain.Tags)
	}

	// AC5 second half: surface tag addable/removable after create.
	created := mustRun(t, testBin, repo, "story", "create",
		"--title", "Later tagged",
		"--body", "Add surface later",
		"--acceptance", "1. tag works",
		"--category", "feature")
	var later struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &later); err != nil {
		t.Fatalf("parse later: %v\n%s", err, created)
	}
	mustRun(t, testBin, repo, "story", "set", later.ID, "--add-tags", "surface:cli")
	got := mustRun(t, testBin, repo, "story", "get", later.ID)
	if !strings.Contains(got, "surface:cli") {
		t.Errorf("add-tags surface:cli should stick:\n%s", got)
	}
	mustRun(t, testBin, repo, "story", "set", later.ID, "--remove-tags", "surface:cli")
	got = mustRun(t, testBin, repo, "story", "get", later.ID)
	if strings.Contains(got, "surface:cli") {
		t.Errorf("remove-tags surface:cli should drop it:\n%s", got)
	}
}
