package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-m", name)
}

func TestRemoteProbes(t *testing.T) {
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	run(t, base, "init", "--bare", "-b", "main", bare)
	tree := filepath.Join(base, "tree")
	run(t, base, "clone", bare, tree)

	// A clone of an empty remote has no commit: Head errors, never panics.
	if _, err := Head(tree); err == nil {
		t.Error("Head on a repo with no commit should error")
	}

	commitFile(t, tree, "one")
	head, err := Head(tree)
	if err != nil || head == "" {
		t.Fatalf("Head = %q, %v", head, err)
	}
	if on, err := OnRemote(tree, head); err != nil || on {
		t.Errorf("OnRemote before push = %v, %v; want false, nil", on, err)
	}

	run(t, tree, "push", "origin", "HEAD:refs/heads/side")
	if on, err := OnRemote(tree, head); err != nil || !on {
		t.Errorf("OnRemote after push to any branch = %v, %v; want true, nil", on, err)
	}

	if dirty, err := Dirty(tree); err != nil || dirty {
		t.Errorf("Dirty on a clean tree = %v, %v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(tree, "loose"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := Dirty(tree); err != nil || !dirty {
		t.Errorf("Dirty with an untracked file = %v, %v; want true", dirty, err)
	}

	// A commit made after the push is not on the remote.
	commitFile(t, tree, "two")
	next, _ := Head(tree)
	if on, _ := OnRemote(tree, next); on {
		t.Error("OnRemote for a new local commit should be false")
	}
}

func TestHasCommit(t *testing.T) {
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	run(t, base, "init", "--bare", "-b", "main", bare)
	tree := filepath.Join(base, "tree")
	run(t, base, "clone", bare, tree)
	commitFile(t, tree, "one")
	head, _ := Head(tree)

	if has, err := HasCommit(tree, head); err != nil || !has {
		t.Errorf("HasCommit(known commit) = %v, %v; want true, nil", has, err)
	}
	if has, err := HasCommit(tree, "0123456789abcdef0123456789abcdef01234567"); err != nil || has {
		t.Errorf("HasCommit(absent sha) = %v, %v; want false, nil", has, err)
	}
	if _, err := HasCommit(t.TempDir(), head); err == nil {
		t.Error("HasCommit outside a repo should error, not report the commit absent")
	}
}

func TestRemoteProbesOutsideARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Dirty(dir); err == nil {
		t.Error("Dirty outside a repo should error")
	}
	if _, err := OnRemote(dir, "abc"); err == nil {
		t.Error("OnRemote outside a repo should error")
	}
}
