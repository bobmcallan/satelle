package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// WorktreeOfMemoised must agree with `git rev-parse --show-toplevel` wherever it
// answers, and decline wherever it cannot confirm the tree.
func TestWorktreeOfMemoised(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE"} {
		t.Setenv(k, "x") // registers the restore; git must not see it unset-to-empty
		os.Unsetenv(k)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(base, "main")
	if err := os.MkdirAll(filepath.Join(main, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "init", "-q")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	linked := filepath.Join(base, "linked")
	gitIn(t, main, "worktree", "add", "-q", "-b", "wt", linked)
	if err := os.MkdirAll(filepath.Join(linked, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(main, "sub", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, nested, "init", "-q")
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(main, "sub"), link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("no memo held declines", func(t *testing.T) {
		if tree, ok := WorktreeOfMemoised(main); ok {
			t.Errorf("answered %q with no identity memoised", tree)
		}
	})

	release := MemoGitIdentity()
	defer release()
	if _, _, ok := gitCommonIdentity(main); !ok { // the call's one rev-parse
		t.Fatal("no git identity for main")
	}

	for _, tc := range []struct {
		name, dir string
	}{
		{"main root", main},
		{"main subdirectory", filepath.Join(main, "sub", "deep")},
		{"linked worktree root", linked},
		{"linked worktree subdirectory", filepath.Join(linked, "pkg")},
		{"through a symlink", link},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := gitIn(t, tc.dir, "rev-parse", "--show-toplevel")
			got, ok := WorktreeOfMemoised(tc.dir)
			if !ok || got != want {
				t.Errorf("WorktreeOfMemoised(%q) = %q, %v; git says %q", tc.dir, got, ok, want)
			}
		})
	}

	for _, tc := range []struct {
		name, dir string
	}{
		{"a repo nested in the tree (identity not memoised)", nested},
		{"outside any repo", other},
	} {
		t.Run("declines "+tc.name, func(t *testing.T) {
			if tree, ok := WorktreeOfMemoised(tc.dir); ok {
				t.Errorf("answered %q for %q; it must leave this to git", tree, tc.dir)
			}
		})
	}

	t.Run("declines when GIT_DIR is set", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(main, ".git"))
		if tree, ok := WorktreeOfMemoised(main); ok {
			t.Errorf("answered %q with GIT_DIR set", tree)
		}
	})
}
