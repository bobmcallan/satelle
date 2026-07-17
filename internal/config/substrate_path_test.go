package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedSubstrateOmitsInRepoStoriesPath (sty_58fa970e AC1): skills and
// principles that ship in the binary must not direct agents to attach/read under
// in-repo .satelle/stories/ as the home. Mentions that forbid the path are OK.
func TestEmbeddedSubstrateOmitsInRepoStoriesPath(t *testing.T) {
	root := filepath.Join("substrate")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		s := string(body)
		// Forbid directing agents to the obsolete attachment home.
		if strings.Contains(s, "under `.satelle/stories/") ||
			strings.Contains(s, "under .satelle/stories/") ||
			strings.Contains(s, "live under `.satelle/stories") {
			t.Errorf("%s still directs agents under .satelle/stories/", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
