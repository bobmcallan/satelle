package fixlane

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

// A lane running in a linked worktree is refused from editing the main tree's
// workflows/agents.toml. The bound keeps the invoking-root classes and adds
// the process substrate as absolute globs.
func TestWorktreeLaneCannotEditMainAgents(t *testing.T) {
	t.Setenv("SATELLE_CONFIG", "")
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
	if err := os.MkdirAll(filepath.Join(main, config.DefaultDataDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(main, config.DefaultDataDir, "workflows", "agents.toml")
	if err := os.WriteFile(agents, []byte("[reviewer]\nrole = \"reviewer\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt", "sty_lane")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "worktree", "add", "-q", "-b", "sty_lane", wt, "HEAD")

	cfg := config.Config{FixLane: config.FixLaneConfig{ProductSurface: []string{"app/**"}}}
	lane := cfg.ResolveFixLane(wt)
	abs := filepath.ToSlash(filepath.Clean(agents))
	if !matchAny(lane.ReviewerRubrics, abs) {
		t.Fatalf("main agents.toml is not a reviewer-rubric glob: %v", lane.ReviewerRubrics)
	}
	db := openStore(t, filepath.Join(t.TempDir(), "s.db"))
	_, err := Record(context.Background(), db.Ledger, cfg, wt, goodInput(agents), time.Now())
	if err == nil {
		t.Fatal("worktree lane was allowed to edit the main tree's workflows/agents.toml")
	}
	if !strings.Contains(err.Error(), "REFUSED") && !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refusal = %v", err)
	}
}
