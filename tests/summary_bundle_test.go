//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSummaryBundleMigratesAndDeclutters drives the real binary to prove the
// per-story implementation summaries are organised into their OKF sub-bundle and
// no longer flood the root documents list (sty_13388123): `satelle index`
// migrates top-level commit-summary-*.md into
// documents/story-implementation-summary/ (its own index.md/log.md), and
// `satelle doc list documents` no longer lists them individually while the
// sub-bundle is surfaced as one root-index entry.
func TestSummaryBundleMigratesAndDeclutters(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	docs := filepath.Join(repo, ".satelle", "documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "commit-summary-sty_aaa.md"), "# Push summary sty_aaa\n\n- x\n")
	writeFile(t, filepath.Join(docs, "commit-summary-sty_bbb.md"), "# Push summary sty_bbb\n\n- y\n")
	writeFile(t, filepath.Join(docs, "real-note.md"), "---\ntype: document\n---\n\n# Real note\n")

	mustRun(t, testBin, repo, "index")

	sub := filepath.Join(docs, "story-implementation-summary")
	for _, n := range []string{"commit-summary-sty_aaa.md", "commit-summary-sty_bbb.md"} {
		if _, err := os.Stat(filepath.Join(docs, n)); !os.IsNotExist(err) {
			t.Errorf("%s was not migrated out of the documents root", n)
		}
		if _, err := os.Stat(filepath.Join(sub, n)); err != nil {
			t.Errorf("%s missing from the sub-bundle: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, "index.md")); err != nil {
		t.Errorf("sub-bundle index.md missing: %v", err)
	}

	// doc list no longer surfaces the summaries individually; the real doc remains.
	out := mustRun(t, testBin, repo, "doc", "list", "documents")
	if strings.Contains(out, "commit-summary-sty_aaa") || strings.Contains(out, "commit-summary-sty_bbb") {
		t.Errorf("doc list is still flooded by the migrated summaries:\n%s", out)
	}
	if !strings.Contains(out, "real-note") {
		t.Errorf("doc list dropped the real document:\n%s", out)
	}

	// root index.md surfaces the sub-bundle as one entry.
	idx, err := os.ReadFile(filepath.Join(docs, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "story-implementation-summary/index.md") {
		t.Errorf("root index.md missing the sub-bundle pointer:\n%s", idx)
	}
}
