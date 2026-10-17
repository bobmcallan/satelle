package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A worktree and its main tree are one repo, so they carry one name.
func TestRepoName_WorktreeAndMainRootAgree(t *testing.T) {
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle-server"))
	wt := filepath.Join(base, "wt", "sty_cafe0001")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, main, wt, "sty_cafe0001")

	if got := RepoName(main); got != "satelle-server" {
		t.Errorf("RepoName(main) = %q, want satelle-server", got)
	}
	if got, want := RepoName(wt), RepoName(main); got != want {
		t.Errorf("RepoName(worktree) = %q, want the main tree's %q", got, want)
	}
}

// A path that is not a git repo is named by its own base.
func TestRepoName_PlainDirIsItsBase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoName(dir); got != "plain" {
		t.Errorf("RepoName = %q, want plain", got)
	}
}
