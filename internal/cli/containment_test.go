package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// sty_a8454d10 AC4: foreign-tree predicate — .git-less temp ALLOWED; sibling
// repo DENIED. Uses real temp dirs (portable: /tmp on Linux, /var/folders on
// macOS — neither has a .git ancestor).

func TestGitRootOf(t *testing.T) {
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(anchor, "internal", "cli", "x.go")
	if got := gitRootOf(deep); got != filepath.Clean(anchor) {
		t.Errorf("gitRootOf deep under anchor: got %q want %q", got, anchor)
	}
	// Worktree form: .git as a file.
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitRootOf(filepath.Join(wt, "f.go")); got != filepath.Clean(wt) {
		t.Errorf("gitRootOf worktree file form: got %q want %q", got, wt)
	}
	// Non-repo temp: no .git up the chain.
	plain := t.TempDir()
	if got := gitRootOf(filepath.Join(plain, "scratch.txt")); got != "" {
		t.Errorf("gitRootOf plain temp: got %q want empty", got)
	}
}

func TestForeignTreeTarget(t *testing.T) {
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sibling, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonRepo := t.TempDir()

	// Sibling repo path → denied, names foreign root.
	path, root, ok := foreignTreeTarget(anchor, []string{filepath.Join(sibling, "main.go")})
	if !ok {
		t.Fatal("sibling repo target must be foreign")
	}
	if path != filepath.Join(sibling, "main.go") {
		t.Errorf("path: got %q", path)
	}
	if root != filepath.Clean(sibling) {
		t.Errorf("foreign root: got %q want %q", root, sibling)
	}

	// Deep path under sibling → denied.
	deep := filepath.Join(sibling, "a", "b", "c.go")
	if _, r, ok := foreignTreeTarget(anchor, []string{deep}); !ok || r != filepath.Clean(sibling) {
		t.Errorf("deep sibling: ok=%v root=%q", ok, r)
	}

	// .git-less temp → ALLOWED (pins the live defect).
	if _, _, ok := foreignTreeTarget(anchor, []string{filepath.Join(nonRepo, "foo.sh")}); ok {
		t.Error("non-repo temp path must be allowed")
	}

	// /dev/null and /dev/fd — no .git ancestor → allowed (allowlist obsolete).
	for _, p := range []string{"/dev/null", "/dev/fd/1"} {
		if _, _, ok := foreignTreeTarget(anchor, []string{p}); ok {
			t.Errorf("%s must be allowed without isBenignOutsidePath", p)
		}
	}

	// Inside anchor → allowed (no foreign).
	if _, _, ok := foreignTreeTarget(anchor, []string{filepath.Join(anchor, "internal", "x.go")}); ok {
		t.Error("in-anchor path must not be foreign")
	}

	// Empty candidates → allow.
	if _, _, ok := foreignTreeTarget(anchor, nil); ok {
		t.Error("empty candidates must allow")
	}
}
