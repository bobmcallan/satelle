package config

import (
	"testing"
)

// sty_6d43eb74 AC1: the scope-free walk reads the [team] files no config area
// owns with the settings area local, while the push's gated share stays empty.
func TestTeamExtraFilesIgnoresScope(t *testing.T) {
	f := newTierFixture(t, "[team]\npaths = [\"skills/**\", \"scripts/*.sh\"]\n"+tierBoundToml, map[string]string{
		"scripts/x.sh": "#!/bin/sh\necho x\n", "scripts/notes.txt": "x\n",
	})
	gated, err := teamExtraFiles(f.cfg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(gated) != 0 {
		t.Errorf("teamExtraFiles with settings local = %v, want none", gated)
	}
	files, err := TeamExtraFiles(f.cfg, f.root, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "scripts/x.sh" || files[0].Area != TeamArea || string(files[0].Content) != "#!/bin/sh\necho x\n" {
		t.Errorf("TeamExtraFiles = %+v, want only scripts/x.sh in the team area", files)
	}
}

// sty_6d43eb74 AC2: the embedded defaults as push files are exactly the
// embedded path→body map, in both directions, sorted by path.
func TestEmbeddedDefaultFiles(t *testing.T) {
	want := embeddedBodyByRel()
	files := EmbeddedDefaultFiles()
	if len(files) == 0 {
		t.Fatal("no embedded default files")
	}
	got := map[string]string{}
	for i, f := range files {
		if i > 0 && files[i-1].Path >= f.Path {
			t.Errorf("not sorted at %d: %s then %s", i, files[i-1].Path, f.Path)
		}
		got[f.Path] = string(f.Content)
	}
	for rel, body := range want {
		if b, ok := got[rel]; !ok {
			t.Errorf("missing %s", rel)
		} else if b != body {
			t.Errorf("%s body differs from the embedded default", rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("extra %s not in the embedded defaults", rel)
		}
	}
}
