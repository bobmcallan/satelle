//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOKFDocumentsNormalizedOnIndex drives the real binary: a frontmatter-less
// concept document dropped into .satelle/documents is normalised to OKF
// frontmatter at `satelle reindex`, and a bundle-root index.md (progressive
// disclosure, okf_version) is generated — the end-to-end OKF conformance path.
func TestOKFDocumentsNormalizedOnIndex(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	docsDir := filepath.Join(repo, ".satelle", "documents")
	// A concept document authored ad-hoc, with no frontmatter.
	raw := "# OKF Demo\n\nA short demo concept document.\n"
	if err := os.WriteFile(filepath.Join(docsDir, "demo.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, testBin, repo, "reindex")

	// The concept file is rewritten in place with OKF frontmatter (required type
	// plus the recommended fields).
	got, err := os.ReadFile(filepath.Join(docsDir, "demo.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"---", "type: document", "title: OKF Demo",
		"description: A short demo concept document.", "timestamp:"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("normalized demo.md missing %q:\n%s", want, got)
		}
	}

	// A bundle-root index.md is generated: okf_version + a relative link to the
	// concept with its description.
	idx, err := os.ReadFile(filepath.Join(docsDir, "index.md"))
	if err != nil {
		t.Fatalf("index.md not generated: %v", err)
	}
	for _, want := range []string{`okf_version: "0.1"`,
		"[OKF Demo](demo.md) - A short demo concept document."} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index.md missing %q:\n%s", want, idx)
		}
	}

	// Re-indexing converges: index.md is reserved and the now-conformant concept
	// is left untouched (no churn, no second rewrite of content).
	mustRun(t, testBin, repo, "reindex")
	got2, _ := os.ReadFile(filepath.Join(docsDir, "demo.md"))
	if string(got2) != string(got) {
		t.Errorf("re-index changed an already-conformant doc:\n%s", got2)
	}
}
