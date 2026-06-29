package config

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEmbeddedSubstrateTagsAreOKF asserts the embedded canonical substrate (the
// defaults that ship in the binary) carries NO legacy `kind:` tag prefix —
// classification lives in the OKF `type:` scalar and `type:` tags (sty_bf2e6ee6).
func TestEmbeddedSubstrateTagsAreOKF(t *testing.T) {
	err := fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return walkErr
		}
		b, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, ln := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "tags:") && strings.Contains(ln, "kind:") {
				t.Errorf("%s: tags carry a legacy kind: prefix — use type: (OKF): %s", p, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
