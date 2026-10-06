package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture builds a main tree that gitignores a directory (dir-only pattern) and
// a file, with both present, and adds a linked worktree.
func fixture(t *testing.T) (main, wt string) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "main")
	if err := os.MkdirAll(filepath.Join(main, ".toolA"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(main, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".toolA/\n.env\n")
	write(".toolA/x", "tool\n")
	write(".env", "SECRET=1\n")
	write("README.md", "r\n")
	run(t, main, "init", "-q")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "config", "user.name", "t")
	run(t, main, "add", "-A")
	run(t, main, "commit", "-q", "-m", "init")
	wt = filepath.Join(base, "wt")
	if err := Open(context.Background(), main, wt, "work/one", "HEAD"); err != nil {
		t.Fatal(err)
	}
	return main, wt
}

func TestCarryLinksIgnoredPathsAndStaysClean(t *testing.T) {
	main, wt := fixture(t)
	rep, err := Carry(context.Background(), main, wt, []string{".toolA", ".env", ".absent"})
	if err != nil {
		t.Fatalf("Carry: %v", err)
	}
	if got := rep.Names(StatusAbsent); len(got) != 1 || got[0] != ".absent" {
		t.Errorf("absent = %v, want [.absent]", got)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, ".toolA", "x")); string(b) != "tool\n" {
		t.Errorf("worktree .toolA/x = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, ".env")); string(b) != "SECRET=1\n" {
		t.Errorf("worktree .env = %q", b)
	}
	for _, dir := range []string{main, wt} {
		if st := run(t, dir, "status", "--porcelain"); st != "" {
			t.Errorf("status in %s not clean:\n%s", dir, st)
		}
	}
	run(t, wt, "add", "-A")
	if st := run(t, wt, "status", "--porcelain"); st != "" {
		t.Errorf("git add -A staged a carried path:\n%s", st)
	}
}

func TestCarryIsIdempotent(t *testing.T) {
	main, wt := fixture(t)
	if _, err := Carry(context.Background(), main, wt, []string{".toolA"}); err != nil {
		t.Fatal(err)
	}
	rep, err := Carry(context.Background(), main, wt, []string{".toolA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Status != StatusAlready {
		t.Errorf("second run = %+v, want already carried", rep.Entries)
	}
	common, _ := CommonDir(context.Background(), main)
	b, _ := os.ReadFile(filepath.Join(common, "info", "exclude"))
	if n := strings.Count(string(b), "/.toolA\n"); n != 1 {
		t.Errorf("exclude holds %d copies of the line:\n%s", n, b)
	}
}

func TestCarryRefusesAPathMainDoesNotIgnore(t *testing.T) {
	main, wt := fixture(t)
	if err := os.WriteFile(filepath.Join(main, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Carry(context.Background(), main, wt, []string{"loose.txt", "README.md"})
	if err == nil || !strings.Contains(err.Error(), `"loose.txt"`) || !strings.Contains(err.Error(), `"README.md"`) {
		t.Fatalf("err = %v, want both unignored entries named", err)
	}
	if _, lerr := os.Lstat(filepath.Join(wt, "loose.txt")); lerr == nil {
		t.Error("an unignored path was linked")
	}
}

func TestCarryLeavesARealDirectoryAlone(t *testing.T) {
	main, wt := fixture(t)
	if err := os.MkdirAll(filepath.Join(wt, ".toolA"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".toolA", "mine"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Carry(context.Background(), main, wt, []string{".toolA"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Status != StatusExists {
		t.Errorf("status = %q, want %q", rep.Entries[0].Status, StatusExists)
	}
	if _, err := os.Stat(filepath.Join(wt, ".toolA", "mine")); err != nil {
		t.Errorf("the hand-made copy was touched: %v", err)
	}
}

// The post-check must name a path the exclude step failed to cover.
func TestCarryPostCheckNamesAPathTheWorktreeDoesNotIgnore(t *testing.T) {
	main, wt := fixture(t)
	orig := excludeWriter
	excludeWriter = func(context.Context, string, []string) error { return nil }
	t.Cleanup(func() { excludeWriter = orig })

	_, err := Carry(context.Background(), main, wt, []string{".toolA"})
	if err == nil || !strings.Contains(err.Error(), `".toolA"`) || !strings.Contains(err.Error(), "not gitignored in the worktree") {
		t.Fatalf("err = %v, want .toolA named as not ignored in the worktree", err)
	}
}

func TestOpenRefusals(t *testing.T) {
	main, _ := fixture(t)
	ctx := context.Background()
	other := filepath.Join(t.TempDir(), "x")
	if err := Open(ctx, main, other, "work/two", "no-such-ref"); err == nil || !strings.Contains(err.Error(), `"no-such-ref"`) {
		t.Errorf("unknown base: %v", err)
	}
	if err := Open(ctx, main, other, "work/one", "HEAD"); err == nil || !strings.Contains(err.Error(), `"work/one"`) {
		t.Errorf("existing branch: %v", err)
	}
	if err := Open(ctx, main, other, "work/three", ""); err == nil {
		t.Error("an empty base was accepted")
	}
	if _, err := os.Lstat(other); err == nil {
		t.Error("a refused open created the path")
	}
}
