//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs a git command in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// gitInitRepo makes dir a git repo with one commit — the precondition for a
// second working tree.
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init")
	gitIn(t, dir, "config", "user.email", "t@t")
	gitIn(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "seed.txt")
	gitIn(t, dir, "commit", "-m", "seed")
}

// addWorktree commits the repo's substrate (a real repo commits .satelle, which
// is how a second working tree is governed at all) and adds a second tree beside
// it. Both trees share one git common dir, so they share one satelle runtime
// plane with no new plumbing.
func addWorktree(t *testing.T, dir string) string {
	t.Helper()
	// Only .satelle — the governing substrate. Deliberately NOT .claude: its
	// settings carry this tree's absolute hook paths, and a second tree that
	// inherited them would fail the scaffolding-staleness check instead of
	// exercising the second tree.
	gitIn(t, dir, "add", "-f", ".satelle")
	gitIn(t, dir, "commit", "-m", "substrate")
	second := filepath.Join(t.TempDir(), "tree-b")
	gitIn(t, dir, "worktree", "add", "-b", "sibling", second)
	return second
}
