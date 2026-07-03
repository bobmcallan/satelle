//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocListLightweight drives the real binary to prove `satelle doc list` is a
// lightweight discovery index (kind/name/headline, no bodies) while `satelle doc
// get` still yields the full body, and the commit-summary bundle is excluded
// (sty_adab13cb).
func TestDocListLightweight(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	docsDir := filepath.Join(repo, ".satelle", "documents")
	body := "---\ntype: document\n---\n\n# Big note\n\n" +
		strings.Repeat("Substantial body prose that must not appear in discovery. ", 40)
	if err := os.WriteFile(filepath.Join(docsDir, "big-note.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A doc named like the commit-summary sub-bundle — must be excluded.
	summary := "---\ntype: document\n---\n\n# Summaries\n\nGenerated commit evidence."
	if err := os.WriteFile(filepath.Join(docsDir, "story-implementation-summary.md"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")

	listOut := mustRun(t, testBin, repo, "doc", "list")
	var docs []map[string]any
	if err := json.Unmarshal([]byte(listOut), &docs); err != nil {
		t.Fatalf("doc list json: %v\n%s", err, listOut)
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[d["name"].(string)] = true
		if b, ok := d["body"]; ok && b != "" {
			t.Errorf("doc list emitted a body for %v", d["name"])
		}
	}
	if !names["big-note"] {
		t.Errorf("authored doc big-note missing from listing: %v", names)
	}
	if names["story-implementation-summary"] {
		t.Error("commit-summary bundle must be excluded from doc list")
	}
	if strings.Contains(listOut, "Substantial body prose") {
		t.Error("doc list must not carry doc bodies")
	}

	// The body is still reachable on demand.
	getOut := mustRun(t, testBin, repo, "doc", "get", "documents", "big-note")
	if !strings.Contains(getOut, "Substantial body prose") {
		t.Errorf("doc get must return the full body:\n%s", getOut)
	}
}
