package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mkSatelleTree makes a satelle-governed repo at dir.
func mkSatelleTree(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// An initial commit: `git worktree add` needs a HEAD to branch from, and a
	// bare `git init` has none.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "t@example.com"},
		{"git", "config", "user.name", "t"},
		{"git", "add", "-A"},
		{"git", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c[1:], err, out)
		}
	}
	return dir
}

// addWorktree makes a linked worktree of main at path.
func addWorktree(t *testing.T, main, path, name string) {
	t.Helper()
	cmd := exec.Command("git", "worktree", "add", "-q", "-b", name, path, "HEAD")
	cmd.Dir = main
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
}

// AC1: a worktree's canonical path is the MAIN working tree's root, and it is
// the same path a command run in the main tree resolves. Without this a worktree
// registers as its own project and relabels the parent's mirror partition.
func TestCanonicalRepoRoot_WorktreeResolvesToMainTree(t *testing.T) {
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle"))
	wt := filepath.Join(base, "wt", "sty_12345678")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, main, wt, "sty_12345678")

	if got, want := CanonicalRepoRoot(wt), CanonicalRepoRoot(main); got != want {
		t.Errorf("CanonicalRepoRoot(worktree) = %q, want the main tree %q", got, want)
	}
	if !strings.HasSuffix(CanonicalRepoRoot(wt), "satelle") {
		t.Errorf("CanonicalRepoRoot(worktree) = %q, want it to end in the main tree's name", CanonicalRepoRoot(wt))
	}
	// The main tree resolves to ITSELF — this must be a no-op for it.
	if got := CanonicalRepoRoot(main); got != main {
		t.Errorf("CanonicalRepoRoot(main) = %q, want %q unchanged", got, main)
	}
}

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// AC1: the canonical path IS the parent of `git rev-parse --path-format=absolute
// --git-common-dir`, resolved from the worktree and from the main tree alike.
func TestCanonicalRepoRoot_WorktreeEqualsMainTreeResolution(t *testing.T) {
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle"))
	wt := filepath.Join(base, "wt", "sty_aaaa1111")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, main, wt, "sty_aaaa1111")

	fromWT := gitOut(t, wt, "rev-parse", "--path-format=absolute", "--git-common-dir")
	fromMain := gitOut(t, main, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if fromWT != fromMain {
		t.Fatalf("git disagrees on the common dir: worktree %q, main %q", fromWT, fromMain)
	}
	if got, want := CanonicalRepoRoot(wt), filepath.Dir(fromWT); got != want {
		t.Errorf("CanonicalRepoRoot(worktree) = %q, want the parent of git's common dir %q", got, want)
	}
	if got, want := CanonicalRepoRoot(wt), CanonicalRepoRoot(main); got != want {
		t.Errorf("worktree resolves to %q, the main tree to %q; they must agree", got, want)
	}
}

// AC1: git < 2.31 rejects --path-format. The resolver must fall back to the bare
// form (which prints a RELATIVE ".git" in a main tree) and still resolve the
// same canonical root. A PATH shim stands in for the old git.
func TestCanonicalRepoRoot_FallsBackWhenGitRejectsPathFormat(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle"))
	wt := filepath.Join(base, "wt", "sty_bbbb2222")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, main, wt, "sty_bbbb2222")
	want := CanonicalRepoRoot(main)

	shimDir := t.TempDir()
	rejected := filepath.Join(shimDir, "rejected")
	shim := "#!/bin/sh\n" +
		"for a in \"$@\"; do case \"$a\" in --path-format*) : > " + rejected + "; echo 'error: unknown option' >&2; exit 129;; esac; done\n" +
		"exec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Skipf("cannot write a git shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := CanonicalRepoRoot(main); got != want {
		t.Errorf("fallback: main tree = %q, want %q", got, want)
	}
	if got := CanonicalRepoRoot(wt); got != want {
		t.Errorf("fallback: worktree = %q, want the main tree %q", got, want)
	}
	if _, err := os.Stat(rejected); err != nil {
		t.Errorf("the shim never rejected --path-format, so the fallback was not exercised: %v", err)
	}
}

// AC6: a non-git directory resolves to the invoking directory, exactly as before.
func TestCanonicalRepoRoot_NonGitDirReturnsInvokingDir(t *testing.T) {
	plain := t.TempDir()
	if got := CanonicalRepoRoot(plain); got != plain {
		t.Errorf("non-git dir = %q, want %q unchanged", got, plain)
	}
	// An empty root means the CWD, so it canonicalises exactly as "." does.
	if got, want := CanonicalRepoRoot(""), CanonicalRepoRoot("."); got != want {
		t.Errorf(`CanonicalRepoRoot("") = %q, want the same as "." = %q`, got, want)
	}
}

// AC6: a git repo with no worktrees is itself, at the path it was invoked with.
func TestCanonicalRepoRoot_GitRepoWithoutWorktreesReturnsItself(t *testing.T) {
	main := mkSatelleTree(t, filepath.Join(t.TempDir(), "solo"))
	if got := CanonicalRepoRoot(main); got != main {
		t.Errorf("git repo without worktrees = %q, want %q unchanged", got, main)
	}
}

// A worktree whose MAIN tree is not satelle-governed must not be redirected to it.
// A documented superset of AC6's fallback.
func TestCanonicalRepoRoot_NonSatelleMainTreeReturnsInvokingDir(t *testing.T) {
	base := t.TempDir()
	nogit := mkSatelleTree(t, filepath.Join(base, "nosatelle"))
	if err := os.RemoveAll(filepath.Join(nogit, DefaultDataDir)); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt2")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, nogit, wt, "wt2")
	if got := CanonicalRepoRoot(wt); got != wt {
		t.Errorf("worktree of a NON-satelle main tree = %q, want %q unchanged", got, wt)
	}
}

// AC7: the DATA dir is deliberately NOT canonicalised. A worktree keeps its own
// .satelle so it can hold authored substrate, which is what lets it derive the
// repo's real route rather than the embedded default. Identity moves; the data
// dir does not.
func TestCanonicalRepoRoot_DoesNotMoveTheDataDir(t *testing.T) {
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle"))
	wt := filepath.Join(base, "wt", "sty_abc")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	// The worktree first, THEN its .satelle: `git worktree add` refuses a
	// non-empty target.
	addWorktree(t, main, wt, "sty_abc")
	if err := os.MkdirAll(filepath.Join(wt, DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// Identity canonicalises...
	if got := CanonicalRepoRoot(wt); got != main {
		t.Fatalf("identity: got %q, want %q", got, main)
	}
	// ...but FindDataDir still resolves the WORKTREE's own .satelle, which is
	// what a worktree needs to read the repo's authored workflows and skills.
	if _, ok := FindDataDir(wt); !ok {
		t.Error("FindDataDir(worktree) found no .satelle; the identity fix must not cost route correctness")
	}
	// And the two roots keep their own substrate directories.
	if wd, _ := FindDataDir(wt); wd != filepath.Join(wt, DefaultDataDir) {
		t.Errorf("worktree data dir = %q, want the worktree's own %q", wd, filepath.Join(wt, DefaultDataDir))
	}
	if md, _ := FindDataDir(main); md != filepath.Join(main, DefaultDataDir) {
		t.Errorf("main data dir = %q, want %q", md, filepath.Join(main, DefaultDataDir))
	}
}

// The runtime dir is keyed by RepoKey, which COLLAPSES worktrees. So the repo.path
// marker it holds is an IDENTITY sink: if it recorded the invoking root it would
// flip to whichever tree opened last, and after `git worktree remove` the MAIN
// repo's whole plane would read as stale and `runtime reap` could offer to delete
// it. A data-loss path, found by satelle-story-architecture-review on
// sty_cd219594's plan edge.
func TestWriteRepoPathMarker_RecordsTheCanonicalRoot(t *testing.T) {
	base := t.TempDir()
	main := mkSatelleTree(t, filepath.Join(base, "satelle"))
	wt := filepath.Join(base, "wt", "sty_feedface")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, main, wt, "sty_feedface")
	if err := os.MkdirAll(filepath.Join(wt, DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}

	plane := filepath.Join(base, "global", RepoKey(main))
	if err := os.MkdirAll(filepath.Dir(plane), 0o755); err != nil {
		t.Fatal(err)
	}
	// A worktree opens the SHARED plane and writes the marker...
	if err := WriteRepoPathMarker(plane, wt); err != nil {
		t.Fatal(err)
	}
	got := ReadRepoPathMarker(plane)
	if got == wt {
		t.Errorf("marker recorded the WORKTREE root %q; the main repo's plane would then read stale after `git worktree remove`", got)
	}
	if want := CanonicalRepoRoot(main); got != want {
		t.Errorf("marker = %q, want the canonical main tree %q", got, want)
	}
}
