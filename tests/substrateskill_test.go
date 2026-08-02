//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// substrateSkillBody resolves a skill's body the way the BINARY does: an
// authored copy under .satelle/skills/ wins, and otherwise the embedded default
// that the doc index overlays at read time.
//
// Tests must not assume an unedited default is materialised on disk. Virtual
// sparse defaults (sty_29e5a9a5) says it should not be — init converges on-disk
// copies but never creates a missing one — and sty_5604e741 deleted this repo's
// stamped shadow copies, at which point four fixtures that read
// `.satelle/skills/<embedded default>.md` directly broke. Reading through this
// helper keeps a fixture honest about where a default actually lives.
func substrateSkillBody(t *testing.T, name string) string {
	t.Helper()
	root := repoRootForTest()
	candidates := []string{
		filepath.Join(root, ".satelle", "skills", name+".md"),
		filepath.Join(root, "internal", "config", "substrate", "skills", name+".md"),
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	t.Fatalf("skill %q resolves nowhere: not authored under .satelle/skills and not an embedded default", name)
	return ""
}
