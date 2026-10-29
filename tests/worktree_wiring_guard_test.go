package tests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_0dca3223 AC4: the remedy the dispatch refusal names — `satelle story
// worktree <id> --existing` — brings a worktree that lacks the pi gate wiring up
// to what the wiring guard requires, with no [worktree] include entry for it.
func TestWorktreeExistingSatisfiesPiWiringGuard(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	main := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".pi/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q", "-b", "main")
	git(main, "config", "user.email", "t@example.com")
	git(main, "config", "user.name", "t")
	git(main, "add", "-A")
	git(main, "commit", "-q", "-m", "A")

	wt := filepath.Join(t.TempDir(), "wt")
	git(main, "worktree", "add", "-q", "-b", "work", wt, "main")

	cfg := config.Config{}
	guard := agentstep.WiringGuardFrom(cfg)
	const want = ".pi/extensions/satelle.ts"
	if got := guard.Missing(wt, "pi", "pi"); !slices.Contains(got, want) {
		t.Fatalf("before --existing: Missing = %v, want it to name %s", got, want)
	}

	ext := filepath.Join(main, filepath.FromSlash(want))
	if err := os.MkdirAll(filepath.Dir(ext), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ext, []byte("// pi extension\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore, _ := verb.SnapshotWiring()
	t.Cleanup(restore)
	verb.SetWorktreeConfig(cfg, main)
	req, _ := json.Marshal(map[string]any{"id": "sty_1", "existing": wt})
	if _, err := verb.Dispatch(context.Background(), "story-worktree", req); err != nil {
		t.Fatalf("story-worktree --existing: %v", err)
	}

	if got := guard.Missing(wt, "pi", "pi"); len(got) != 0 {
		t.Errorf("after --existing: Missing = %v, want empty", got)
	}
}
