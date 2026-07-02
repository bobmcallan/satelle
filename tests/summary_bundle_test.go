//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSummariesLiveWithTheStory drives the real binary to prove implementation
// summaries live WITH their story (sty_97c53d72): `satelle reindex` migrates
// legacy summaries — the retired documents/story-implementation-summary
// sub-bundle and any root documents/commit-summary-sty_*.md — into the owning
// story's attachment folder .satelle/stories/<id>/, removes the emptied
// sub-bundle, and the artifacts surface via `satelle story docs <id>`.
func TestSummariesLiveWithTheStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)

	// Two stories the legacy summaries belong to.
	idA := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "A", "--body", "goal", "--acceptance", "1. a", "--category", "feature"), "sty_")
	idB := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "B", "--body", "goal", "--acceptance", "1. b", "--category", "feature"), "sty_")

	docs := filepath.Join(repo, ".satelle", "documents")
	sub := filepath.Join(docs, "story-implementation-summary")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy sub-bundle summary + a legacy root summary + a normal doc.
	writeFile(t, filepath.Join(sub, "commit-summary-"+idA+".md"), "---\nstory: "+idA+"\ntype: story-implementation-summary\nname: commit-summary-"+idA+"\n---\n\n# Push summary A\n")
	writeFile(t, filepath.Join(docs, "commit-summary-"+idB+".md"), "---\nstory: "+idB+"\ntype: story-implementation-summary\nname: commit-summary-"+idB+"\n---\n\n# Push summary B\n")
	writeFile(t, filepath.Join(docs, "real-note.md"), "---\ntype: document\n---\n\n# Real note\n")

	mustRun(t, testBin, repo, "reindex")

	// Each summary migrated into its story's attachment folder.
	for id, n := range map[string]string{idA: "commit-summary-" + idA + ".md", idB: "commit-summary-" + idB + ".md"} {
		if _, err := os.Stat(filepath.Join(repo, ".satelle", "stories", id, n)); err != nil {
			t.Errorf("summary %s not migrated to story %s: %v", n, id, err)
		}
	}
	// The retired sub-bundle is gone; the real doc stays.
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("retired sub-bundle should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, "real-note.md")); err != nil {
		t.Errorf("non-summary doc must not move: %v", err)
	}
	// The migrated artifact surfaces on the story.
	if out := mustRun(t, testBin, repo, "story", "docs", idA); !strings.Contains(out, "commit-summary-"+idA) {
		t.Errorf("migrated summary not listed by story docs:\n%s", out)
	}
}

// TestAttachFileAndDropAllocation proves the two artifact paths (sty_97c53d72):
// `story attach --file` reads the body from a file, and a hand-DROPPED .md with
// correct frontmatter in .satelle/stories/<id>/ is allocated to the story by
// construction (story docs/doc read the directory).
func TestAttachFileAndDropAllocation(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)

	id := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "S", "--body", "goal", "--acceptance", "1. s", "--category", "feature"), "sty_")

	// attach --file
	src := filepath.Join(repo, "summary.md")
	writeFile(t, src, "# Implementation summary\n\n- shipped the thing\n")
	mustRun(t, testBin, repo, "story", "attach", id, "--name", "implementation-summary", "--type", "story-implementation-summary", "--file", src)
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "stories", id, "implementation-summary.md")); err != nil {
		t.Fatalf("attach --file did not write the artifact: %v", err)
	}
	got := mustRun(t, testBin, repo, "story", "doc", id, "implementation-summary")
	if !strings.Contains(got, "shipped the thing") || !strings.Contains(got, "story-implementation-summary") {
		t.Errorf("attached artifact content/type wrong:\n%s", got)
	}

	// hand-dropped artifact with correct frontmatter → allocated (listed).
	drop := "---\nstory: " + id + "\ntype: evidence\nname: hand-dropped\n---\n\n# Dropped\n"
	writeFile(t, filepath.Join(repo, ".satelle", "stories", id, "hand-dropped.md"), drop)
	if out := mustRun(t, testBin, repo, "story", "docs", id); !strings.Contains(out, "hand-dropped") {
		t.Errorf("hand-dropped artifact not allocated to the story:\n%s", out)
	}
	// --body and --file are mutually exclusive.
	if out, err := run(t, testBin, repo, "story", "attach", id, "--name", "x", "--body", "b", "--file", src); err == nil {
		t.Errorf("--body with --file should be rejected:\n%s", out)
	}
}
