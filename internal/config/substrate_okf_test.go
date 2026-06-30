package config

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
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

// TestEmbeddedSubstrateStructure runs the deterministic structure check on every
// embedded substrate doc — the canonical defaults that ship in the binary must be
// OKF/structure-conformant (sty_31069c05). This also covers the embedded layer
// that the file-based `satelle validate` (which walks .satelle/) does not see.
func TestEmbeddedSubstrateStructure(t *testing.T) {
	err := fs.WalkDir(substrateFS, "substrate", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return walkErr
		}
		base := path.Base(p)
		if base == "README.md" || base == "index.md" || base == "log.md" {
			return nil // reserved keep-files are exempt
		}
		kind := path.Base(path.Dir(p)) // substrate/<kind>/<file>.md
		if !structure.Checked(kind) {
			return nil
		}
		b, rerr := substrateFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		name := strings.TrimSuffix(base, ".md")
		for _, prob := range structure.Doc(kind, name, string(b), nil) {
			t.Errorf("embedded %s/%s: %s", kind, name, prob)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
