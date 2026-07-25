//go:build integration

package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestCategoryVocabulary (sty_b2315e17): default warn surfaces the VIRE defect
// case on create; reject mode hard-fails; casing canonicalises; legacy
// categories are never force-migrated on terminal stories.
func TestCategoryVocabulary(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Disable LLM create gate so structure-only path exercises the vocab check.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = false\n")

	// Default enforce is warn: create with --category defect succeeds and names
	// the allowed list (including fix) on the combined out (stderr notice).
	out, err := run(t, testBin, repo, "story", "create",
		"--title", "VIRE invented category case",
		"--body", "Reproduce inventing a synonym category under warn mode.",
		"--acceptance", "1. warn names the unknown value and the survivor fix",
		"--category", "defect",
	)
	if err != nil {
		t.Fatalf("warn mode should still create: %v\n%s", err, out)
	}
	// Distinct notice marker — not present in title/body — so the assert is not vacuous.
	if !strings.Contains(out, "not in the controlled vocabulary") {
		t.Fatalf("warn notice must fire for defect:\n%s", out)
	}
	if !strings.Contains(out, "defect") || !strings.Contains(out, "fix") {
		t.Fatalf("warn notice must name defect and list fix:\n%s", out)
	}
	var created struct {
		ID       string `json:"id"`
		Category string `json:"category"`
	}
	if jerr := json.Unmarshal([]byte(firstJSONObject(out)), &created); jerr != nil {
		t.Fatalf("parse create: %v\nout:\n%s", jerr, out)
	}
	if created.Category != "defect" {
		t.Errorf("warn mode stores unknown category as-is, got %q", created.Category)
	}

	// Case: Fix → stored as fix.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Cased fix",
		"--body", "Case canonicalisation probe",
		"--acceptance", "1. stored as fix",
		"--category", "Fix",
	)
	var cased struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &cased); err != nil {
		t.Fatalf("parse cased: %v\n%s", err, out)
	}
	if cased.Category != "fix" {
		t.Errorf("want category fix, got %q", cased.Category)
	}

	// Reject mode: defect fails closed with allowed list.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"reject\"\n")
	out, err = run(t, testBin, repo, "story", "create",
		"--title", "Reject defect",
		"--body", "Should fail under reject",
		"--acceptance", "1. n/a",
		"--category", "defect",
	)
	if err == nil {
		t.Fatalf("reject mode should fail on defect; out:\n%s", out)
	}
	for _, want := range []string{"defect", "fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject error should name %q:\n%s", want, out)
		}
	}

	// AC4: legacy category under off, then flip reject — get still shows the
	// original category; a non-category set does not trip reject; explicit
	// --category defect still fails. No force migration of existing rows.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Legacy process category",
		"--body", "A pre-vocabulary category that must survive",
		"--acceptance", "1. never force-migrated",
		"--category", "process",
	)
	var legacy struct {
		ID       string `json:"id"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &legacy); err != nil {
		t.Fatalf("parse legacy: %v\n%s", err, out)
	}
	if legacy.Category != "process" {
		t.Fatalf("want process, got %q", legacy.Category)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"reject\"\n")
	got := mustRun(t, testBin, repo, "story", "get", legacy.ID)
	if !strings.Contains(got, `"category": "process"`) && !strings.Contains(got, `"category":"process"`) {
		t.Errorf("legacy category must not be rewritten:\n%s", got)
	}
	if _, err := run(t, testBin, repo, "story", "set", legacy.ID, "--priority", "low"); err != nil {
		t.Fatalf("non-category set on legacy category must work: %v", err)
	}
	if _, err := run(t, testBin, repo, "story", "set", legacy.ID, "--category", "defect"); err == nil {
		t.Fatal("set --category defect under reject must fail")
	}
}

// firstJSONObject returns the first {...} JSON object in s (stdout may be
// followed by stderr notices when combined).
func firstJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}
