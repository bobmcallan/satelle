//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryBacklogOKFReference drives the real binary to prove `satelle reindex`
// materializes the read-only OKF backlog reference under .satelle/stories/
// (sty_3c7c043d): open stories become generated concept files + index.md/log.md,
// and a planted legacy flat mirror leftover is pruned. The DB stays the sole
// store; this is a regenerated view.
func TestStoryBacklogOKFReference(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "story", "create", "--title", "Backlog A", "--body", "alpha body")
	mustRun(t, testBin, repo, "story", "create", "--title", "Backlog B", "--body", "beta body")

	storiesDir := filepath.Join(repo, ".satelle", "stories")
	if err := os.MkdirAll(storiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// a legacy flat mirror leftover (no generated marker) that index must prune.
	if err := os.WriteFile(filepath.Join(storiesDir, "sty_deadbeef.md"), []byte("# stale legacy mirror"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, testBin, repo, "reindex")

	// index.md is a backlog reference listing both open stories.
	idx, err := os.ReadFile(filepath.Join(storiesDir, "index.md"))
	if err != nil {
		t.Fatalf("no index.md: %v", err)
	}
	if !strings.Contains(string(idx), "# Backlog") {
		t.Errorf("index.md is not the backlog reference:\n%s", idx)
	}
	// log.md exists (the OKF changelog).
	if _, err := os.Stat(filepath.Join(storiesDir, "log.md")); err != nil {
		t.Errorf("no log.md: %v", err)
	}
	// two generated per-story files, each carrying the generated marker.
	got := 0
	ents, _ := os.ReadDir(storiesDir)
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "sty_") || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(storiesDir, e.Name()))
		if strings.Contains(string(b), "generated: satelle") {
			got++
		}
	}
	if got != 2 {
		t.Errorf("expected 2 generated story files, got %d", got)
	}
	// the legacy leftover was pruned.
	if _, err := os.Stat(filepath.Join(storiesDir, "sty_deadbeef.md")); !os.IsNotExist(err) {
		t.Errorf("legacy flat mirror leftover sty_deadbeef.md was not pruned")
	}
}
