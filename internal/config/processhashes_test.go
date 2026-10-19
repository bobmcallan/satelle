package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findingPaths(fs []ProcessFinding) map[string]string {
	out := map[string]string{}
	for _, f := range fs {
		out[f.Path] = f.Kind
	}
	return out
}

// ProcessHashes covers every authored substrate dir and the constitution, keyed
// relative to the process root, and DiffTreeHashes names a file edited under a
// substrate root and the edited constitution.
func TestProcessHashesAndDiffTreeHashes(t *testing.T) {
	root := t.TempDir()
	var cfg Config
	dirs := ResolveProcessAuthoredDirs(cfg, root)
	skill := filepath.Join(dirs["skills"], "plan.md")
	principle := filepath.Join(dirs["principles"], "p.md")
	constitution := ResolveProcessConstitution(cfg, root)
	writeAt(t, skill, "one")
	writeAt(t, principle, "p")
	writeAt(t, constitution, "c")

	before := ProcessHashes(cfg, root)
	skillRel, _ := plainRelUnder(root, skill)
	principleRel, _ := plainRelUnder(root, principle)
	constitutionRel, _ := plainRelUnder(root, constitution)
	for _, rel := range []string{skillRel, principleRel, constitutionRel} {
		if _, ok := before[filepath.ToSlash(rel)]; !ok {
			t.Errorf("ProcessHashes lacks %s: %v", rel, before)
		}
	}
	if got := DiffTreeHashes(before, ProcessHashes(cfg, root)); len(got) != 0 {
		t.Errorf("an unchanged tree diffs: %v", got)
	}

	writeAt(t, skill, "two")
	writeAt(t, constitution, "c2")
	writeAt(t, filepath.Join(dirs["skills"], "added.md"), "x")
	if err := os.Remove(principle); err != nil {
		t.Fatal(err)
	}
	got := findingPaths(DiffTreeHashes(before, ProcessHashes(cfg, root)))
	want := map[string]string{
		filepath.ToSlash(skillRel):                                          ProcessChanged,
		filepath.ToSlash(constitutionRel):                                   ProcessChanged,
		filepath.ToSlash(filepath.Join(filepath.Dir(skillRel), "added.md")): ProcessExtra,
		filepath.ToSlash(principleRel):                                      ProcessMissing,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

// A substrate root outside the process root is shared, not process-local, and is
// left out of the map.
func TestProcessHashesLeavesOutRootsOutsideTheProcessRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeAt(t, filepath.Join(outside, "skills", "s.md"), "s")
	cfg := Config{SubstrateRoots: map[string]string{"skills": outside}}
	writeAt(t, ResolveProcessConstitution(cfg, root), "c")
	for p := range ProcessHashes(cfg, root) {
		if filepath.Base(p) == "s.md" {
			t.Errorf("a root outside the process root is in the map: %s", p)
		}
	}
}
