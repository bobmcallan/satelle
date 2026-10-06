//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcessDataDirFollowsMainTree (sty_a0140039) proves the shared helper
// resolves the process of record the way satelle does: from a linked worktree
// that has no .satelle of its own it returns the MAIN tree's data dir — the
// same one the main tree returns — and the lookup creates nothing in the
// worktree.
func TestProcessDataDirFollowsMainTree(t *testing.T) {
	main := t.TempDir()
	mustRun(t, testBin, main, "init")
	// The linked worktree must NOT inherit .satelle, so only a seed file is
	// committed (gitInitRepo), unlike addWorktree which commits the substrate.
	gitInitRepo(t, main)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, main, "worktree", "add", "-b", "story", wt)

	if _, err := os.Stat(filepath.Join(wt, ".satelle")); !os.IsNotExist(err) {
		t.Fatalf("fixture worktree must start without a .satelle (stat err = %v)", err)
	}

	want := filepath.Join(main, ".satelle")
	for name, root := range map[string]string{"main": main, "worktree": wt} {
		got := processDataDirFor(root)
		if !sameDir(t, got, want) {
			t.Errorf("processDataDirFor(%s tree) = %s, want the main tree's data dir %s", name, got, want)
		}
	}

	// A file authored in the main tree is the one the worktree reads.
	authored := filepath.Join(want, "workflows", "marker.toml")
	writeFile(t, authored, "marker = true\n")
	b, err := os.ReadFile(filepath.Join(processDataDirFor(wt), "workflows", "marker.toml"))
	if err != nil || string(b) != "marker = true\n" {
		t.Errorf("worktree read of the main tree's authored file = %q, %v", b, err)
	}

	// The data dir the main tree's config names wins over the default.
	cfgPath := filepath.Join(want, "satelle.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(main, "custom-data")
	if err := os.WriteFile(cfgPath, append([]byte("data_dir = \"custom-data\"\n"), cfg...), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{"main": main, "worktree": wt} {
		if got := processDataDirFor(root); !sameDir(t, got, named) {
			t.Errorf("processDataDirFor(%s tree) = %s, want the config-named data dir %s", name, got, named)
		}
	}

	// Reading the process of record never creates a .satelle in the worktree.
	if _, err := os.Stat(filepath.Join(wt, ".satelle")); !os.IsNotExist(err) {
		t.Errorf("the worktree gained a .satelle after the helper ran (stat err = %v)", err)
	}
}

// sameDir compares two paths after resolving symlinks, so a temp dir reached
// through a symlinked parent still matches.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil {
		ra = filepath.Clean(a)
	}
	if errB != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}
