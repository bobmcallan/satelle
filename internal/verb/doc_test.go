package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoc writes an OKF-ish markdown file with a substantial body so the
// full-vs-lightweight size difference is meaningful.
func writeDoc(t *testing.T, dir, name, typ, headline string) {
	t.Helper()
	body := "---\ntype: " + typ + "\n---\n\n# " + headline + "\n\n" +
		strings.Repeat("Filler prose that stands in for a real document body. ", 40)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestDocListLightweightByDefault asserts the default doc-list is a lightweight
// index (kind/name/headline, no bodies, commit-summary bundle excluded) and that
// full=true still returns whole records for body-consuming callers (sty_adab13cb).
func TestDocListLightweightByDefault(t *testing.T) {
	wire(t)

	docsDir := t.TempDir()
	writeDoc(t, docsDir, "alpha-note", "documents", "Alpha note")
	writeDoc(t, docsDir, "beta-note", "documents", "Beta note")
	// A doc named like the commit-summary sub-bundle must be excluded from the
	// default discovery listing (AC2).
	writeDoc(t, docsDir, "story-implementation-summary", "documents", "Summaries")

	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"documents": docsDir}})

	// Default (lightweight) listing.
	lightRaw := call(t, "doc-list", nil)
	var light []map[string]any
	if err := json.Unmarshal(lightRaw, &light); err != nil {
		t.Fatalf("unmarshal light: %v", err)
	}

	names := map[string]bool{}
	for _, d := range light {
		names[d["name"].(string)] = true
		// AC1: no body in the lightweight projection.
		if b, ok := d["body"]; ok && b != "" {
			t.Errorf("lightweight listing carries a body for %v: %q", d["name"], b)
		}
		if d["headline"] == nil || d["headline"] == "" {
			t.Errorf("lightweight listing missing headline for %v", d["name"])
		}
	}
	if !names["alpha-note"] || !names["beta-note"] {
		t.Errorf("expected authored docs in listing, got %v", names)
	}
	// AC2: the commit-summary bundle is excluded.
	if names["story-implementation-summary"] {
		t.Error("commit-summary bundle must not appear in the default listing")
	}

	// full=true returns whole records with bodies.
	fullRaw := call(t, "doc-list", map[string]any{"full": true})
	var full []map[string]any
	if err := json.Unmarshal(fullRaw, &full); err != nil {
		t.Fatalf("unmarshal full: %v", err)
	}
	sawBody := false
	for _, d := range full {
		if b, _ := d["body"].(string); strings.Contains(b, "Filler prose") {
			sawBody = true
		}
	}
	if !sawBody {
		t.Error("full=true listing must carry doc bodies")
	}

	// AC3: the default listing is a small fraction of the full-body output.
	if len(lightRaw)*3 >= len(fullRaw) {
		t.Errorf("lightweight listing not materially smaller: light=%d full=%d", len(lightRaw), len(fullRaw))
	}
}
