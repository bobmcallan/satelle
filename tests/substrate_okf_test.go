//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoSubstrateTagsAreOKF asserts THIS repo's authored substrate (.satelle
// and the embedded substrate source) carries no legacy kind: tag prefix
// (sty_bf2e6ee6) — OKF uses type:.
func TestRepoSubstrateTagsAreOKF(t *testing.T) {
	root := repoRootForTest()
	for _, dir := range []string{
		filepath.Join(root, ".satelle"),
		filepath.Join(root, "internal", "config", "substrate"),
	} {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, ln := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(ln), "tags:") && strings.Contains(ln, "kind:") {
					t.Errorf("%s: tags carry a legacy kind: prefix — use type:: %s", p, strings.TrimSpace(ln))
				}
			}
			return nil
		})
	}
}
