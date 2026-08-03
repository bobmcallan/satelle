//go:build !integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildVersionScript proves scripts/build-version.sh (the stamp make
// install feeds into ldflags) without reading the Makefile (sty_022929ef AC1).
func TestBuildVersionScript(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "build-version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		// Running from repo root (go test ./tests/…)
		script, err = filepath.Abs(filepath.Join("scripts", "build-version.sh"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("build-version.sh not found: %v", err)
	}

	run := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("build-version.sh %v in %s: %v\n%s", args, dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Non-git directory → base unchanged.
	t.Run("non-git", func(t *testing.T) {
		dir := t.TempDir()
		if got := run(t, dir, "9.9.9"); got != "9.9.9" {
			t.Fatalf("got %q, want 9.9.9", got)
		}
	})

	// Fresh repo at a tagged release commit, clean.
	t.Run("clean-at-tag", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
		git(t, dir, "tag", "v9.9.9")
		got := run(t, dir, "9.9.9")
		if got != "9.9.9" {
			t.Fatalf("clean at tag: got %q, want 9.9.9", got)
		}
	})

	t.Run("dirty-at-tag", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
		git(t, dir, "tag", "v9.9.9")
		if err := os.WriteFile(filepath.Join(dir, "dirt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := run(t, dir, "9.9.9")
		if !strings.HasPrefix(got, "9.9.9+") || !strings.HasSuffix(got, "-dirty") {
			t.Fatalf("dirty at tag: got %q, want 9.9.9+<sha>-dirty", got)
		}
	})

	t.Run("clean-past-tag", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
		git(t, dir, "tag", "v9.9.9")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "next")
		got := run(t, dir, "9.9.9")
		if !strings.HasPrefix(got, "9.9.9+") || strings.HasSuffix(got, "-dirty") {
			t.Fatalf("past tag: got %q, want 9.9.9+<sha>", got)
		}
	})

	t.Run("no-tag", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
		got := run(t, dir, "9.9.9")
		if !strings.HasPrefix(got, "9.9.9+") {
			t.Fatalf("no tag: got %q, want 9.9.9+<sha>", got)
		}
	})

	t.Run("serve-prefix", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init")
		git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
		git(t, dir, "tag", "serve-v0.0.5")
		got := run(t, dir, "0.0.5", "serve-v")
		if got != "0.0.5" {
			t.Fatalf("serve clean at tag: got %q, want 0.0.5", got)
		}
	})
}
