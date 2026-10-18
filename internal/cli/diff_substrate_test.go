package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A story that changes only gitignored substrate must hand the reviewer those
// files and their content through the reviewer-payload resolver (sty_98a2816b
// AC1): this repo keeps .satelle/ out of git, so the git slice alone is empty.
func TestDiffResolverCarriesGitignoredSubstrate(t *testing.T) {
	e := closeGuardEnv(t)
	e.write(".gitignore", ".satelle/\n")
	e.git("rm", "-r", "--cached", "-q", ".satelle")
	e.git("add", ".gitignore")
	e.git("commit", "-qm", "ignore substrate")

	id := e.engage()
	rel := ".satelle/skills/reviewer-visible.md"
	e.write(rel, "# reviewer visible\nthe instruction text under review\n")
	// Past the baseline whatever the filesystem clock's grain.
	at := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(e.repo, rel), at, at); err != nil {
		t.Fatal(err)
	}

	d := diffResolver()(context.Background(), id)
	if d == nil || d.NoBaseline {
		t.Fatalf("a baselined story must resolve a diff, got %+v", d)
	}
	found := false
	for _, f := range d.Files {
		found = found || f == rel
	}
	if !found {
		t.Errorf("files must list the gitignored substrate path %s: %v", rel, d.Files)
	}
	for _, want := range []string{
		"+++ b/" + rel + "\n", "+# reviewer visible\n", "+the instruction text under review\n", "untracked",
	} {
		if !strings.Contains(d.Patch, want) {
			t.Errorf("patch missing %q:\n%s", want, d.Patch)
		}
	}
}
