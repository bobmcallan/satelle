//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStoryRetentionEndToEnd drives the real binary to prove archive retention
// (sty_aba7200c): with stories_keep_closed configured, reindex prunes closed
// stories' attachment dirs beyond the policy to .satelle/backups/stories/, keeps
// the most-recent closed dir, and ALWAYS keeps a non-terminal story's dir.
func TestStoryRetentionEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Configure a count policy of 1 (keep the single most-recent closed dir).
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	orig, _ := os.ReadFile(tomlPath)
	if err := os.WriteFile(tomlPath, append(orig, []byte("\nstories_keep_closed = 1\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	// Three closed stories (created oldest→newest) and one open story, each with an
	// attachment dir carrying evidence.
	storyDir := filepath.Join(repo, ".satelle", "stories")
	closed1 := createStory(t, repo, "S", "done")
	closed2 := createStory(t, repo, "S", "done")
	closed3 := createStory(t, repo, "S", "done")
	open := createStory(t, repo, "S", "backlog")
	for _, id := range []string{closed1, closed2, closed3, open} {
		d := filepath.Join(storyDir, id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "summary.md"), []byte("evidence for "+id), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustRun(t, testBin, repo, "reindex")

	exists := func(id string) bool {
		fi, err := os.Stat(filepath.Join(storyDir, id))
		return err == nil && fi.IsDir()
	}
	// The most-recent closed dir and the open story's dir are kept.
	if !exists(closed3) {
		t.Error("the most-recent closed dir must be kept")
	}
	if !exists(open) {
		t.Error("a non-terminal story's dir must always be kept")
	}
	// The two older closed dirs are pruned from .satelle/stories …
	if exists(closed1) || exists(closed2) {
		t.Error("closed dirs beyond the retention count must be pruned")
	}
	// … and MOVED to the mandatory backup (not deleted).
	backups := filepath.Join(repo, ".satelle", "backups", "stories")
	if _, err := os.Stat(backups); err != nil {
		t.Errorf("retention did not write a backup under %s: %v", backups, err)
	}
	if findFileInTree(t, backups, "summary.md") == "" {
		t.Error("pruned dir's evidence not found under the backup tree")
	}
}

// findFileInTree walks root for a file with the given base name.
func findFileInTree(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.Name() == name {
			found = p
		}
		return nil
	})
	return found
}
