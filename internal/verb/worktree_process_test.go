package verb

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Opening a linked worktree and wiring SetEngagementMode from the process
// config yields the main tree's seat key. The decoy mode would key the child
// by its own id.
func TestWorktreeSeatKeyUsesProcessConfig(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_CONFIG", "")
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	t.Cleanup(ClearEngagementMode)

	base := t.TempDir()
	main := filepath.Join(base, "proj")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(main, "init", "-q")
	git(main, "config", "user.email", "t@example.com")
	git(main, "config", "user.name", "t")
	git(main, "add", "-A")
	git(main, "commit", "-q", "-m", "init")
	if err := os.MkdirAll(filepath.Join(main, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[engagement]\nparallel = \"epic\"\n"
	if err := os.WriteFile(filepath.Join(main, ".satelle", "satelle.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt", "sty_seat")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "worktree", "add", "-q", "-b", "sty_seat", wt, "HEAD")
	decoy := "[engagement]\nparallel = \"none\"\n"
	if err := os.MkdirAll(filepath.Join(wt, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".satelle", "satelle.toml"), []byte(decoy), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wt)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if a.ProcessConfig.ResolveEngagementParallel() != config.ParallelEpic {
		t.Fatalf("process mode = %q", a.ProcessConfig.ResolveEngagementParallel())
	}
	SetEngagementMode(a.ProcessConfig)
	got := seatKeyFor(workitem.Item{ID: "sty_child", ParentID: "sty_epic", Category: "feature"})
	if got != "sty_epic" {
		t.Fatalf("seat key = %q, want the parent id from the main tree's epic mode", got)
	}
}
