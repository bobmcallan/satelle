//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCancelReasonVocabulary (sty_de8e8e2c AC-1, AC-7): [tags.vocabulary]
// cancel-reason rejects undeclared values, accepts each declared value with
// case-canonical storage, and leaves repos without the namespace free-form.
func TestCancelReasonVocabulary(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n[tags.vocabulary]\ncancel-reason = [\"shelved\", \"duplicate\", \"superseded\", \"withdrawn\", \"wrong\"]\n"
	if err := os.WriteFile(cfgPath, append(cfg, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = false\n")

	// Undeclared value rejected with named error listing allowed set.
	out, err := run(t, testBin, repo, "story", "create",
		"--title", "Bad cancel-reason",
		"--body", "Should fail vocab",
		"--acceptance", "1. n/a",
		"--category", "chore",
		"--tags", "cancel-reason:parked")
	if err == nil {
		t.Fatalf("cancel-reason:parked should be rejected; out:\n%s", out)
	}
	for _, want := range []string{"cancel-reason", "parked", "shelved", "duplicate", "superseded", "withdrawn", "wrong"} {
		if !strings.Contains(out, want) {
			t.Errorf("reject error should name %q:\n%s", want, out)
		}
	}

	// Each declared value accepts and round-trips.
	for _, val := range []string{"shelved", "duplicate", "superseded", "withdrawn", "wrong"} {
		out = mustRun(t, testBin, repo, "story", "create",
			"--title", "Cancel "+val,
			"--body", "Reason: testing "+val,
			"--acceptance", "1. tagged",
			"--category", "chore",
			"--tags", "cancel-reason:"+val)
		var created struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal([]byte(out), &created); err != nil {
			t.Fatalf("parse create %s: %v\n%s", val, err, out)
		}
		want := "cancel-reason:" + val
		found := false
		for _, tg := range created.Tags {
			if tg == want {
				found = true
			}
		}
		if !found {
			t.Errorf("want tag %q on create, got %v", want, created.Tags)
		}
	}

	// Case-insensitive match → stored in declared casing.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Cased shelved",
		"--body", "Reason: case probe",
		"--acceptance", "1. canonical",
		"--category", "chore",
		"--tags", "cancel-reason:SHELVED")
	var cased struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &cased); err != nil {
		t.Fatalf("parse cased: %v\n%s", err, out)
	}
	foundCanon, foundUpper := false, false
	for _, tg := range cased.Tags {
		if tg == "cancel-reason:shelved" {
			foundCanon = true
		}
		if tg == "cancel-reason:SHELVED" {
			foundUpper = true
		}
	}
	if !foundCanon {
		t.Fatalf("want cancel-reason:shelved stored, got %v", cased.Tags)
	}
	if foundUpper {
		t.Errorf("stored non-canonical cancel-reason:SHELVED")
	}

	// Repo with no cancel-reason namespace: free-form value accepted.
	repo2 := t.TempDir()
	mustRun(t, testBin, repo2, "init")
	writeFile(t, filepath.Join(repo2, ".satelle", "satelle.local.toml"), "[review]\ngate_create = false\n")
	// Ensure no cancel-reason key even if init seeds surface.
	cfg2, err := os.ReadFile(filepath.Join(repo2, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg2), "cancel-reason") {
		// Strip any accidental declaration for this negative case.
		cleaned := strings.ReplaceAll(string(cfg2), "cancel-reason", "x-cancel-reason-disabled")
		if err := os.WriteFile(filepath.Join(repo2, ".satelle", "satelle.toml"), []byte(cleaned), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out = mustRun(t, testBin, repo2, "story", "create",
		"--title", "Free-form cancel-reason",
		"--body", "No vocab declared",
		"--acceptance", "1. free",
		"--category", "chore",
		"--tags", "cancel-reason:anything-goes")
	if !strings.Contains(out, "cancel-reason:anything-goes") {
		t.Errorf("undeclared namespace should accept free-form tag:\n%s", out)
	}
}
