package agentstep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_ab93f9a6: a functional check runs in the main tree (the process of record)
// and is told which story it judges, so a story engaged from a linked worktree
// is reached through that story and not by moving the check.

// The check's working directory stays the repo root, and the gated story rides
// the context the check is run with.
func TestRunCheckKeepsRepoRootAndNamesStory(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.repoRoot = "/the/main/tree"
	var gotDir, gotStory string
	g.check = func(ctx context.Context, dir, _, _ string) (string, error) {
		gotDir, gotStory = dir, gateStoryFrom(ctx)
		return "", nil
	}
	if dec := g.runCheck(context.Background(), "sty_wt", "satelle-x-check", "true", "{}"); !dec.Accept {
		t.Fatalf("check should accept: %+v", dec)
	}
	if gotDir != g.repoRoot {
		t.Errorf("check dir = %q, want the repo root %q", gotDir, g.repoRoot)
	}
	if gotStory != "sty_wt" {
		t.Errorf("check story = %q, want sty_wt", gotStory)
	}
}

// execCheck exports the story to the check's environment and leaves the cwd
// alone; with no story marked, the environment carries no gate context.
func TestExecCheckExportsGateStory(t *testing.T) {
	dir := t.TempDir()
	want, _ := filepath.EvalSymlinks(dir)
	script := `echo "story=${` + verb.GateStoryEnv + `:-none}"; pwd -P`

	out, err := execCheck(withGateStory(context.Background(), "sty_wt"), dir, script, "")
	if err != nil {
		t.Fatalf("execCheck: %v\n%s", err, out)
	}
	if !strings.Contains(out, "story=sty_wt") {
		t.Errorf("check must see the gated story: %q", out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("check cwd must stay %q: %q", want, out)
	}

	if _, ok := os.LookupEnv(verb.GateStoryEnv); ok {
		t.Skipf("%s set in the test environment", verb.GateStoryEnv)
	}
	out, err = execCheck(context.Background(), dir, script, "")
	if err != nil {
		t.Fatalf("execCheck: %v\n%s", err, out)
	}
	if !strings.Contains(out, "story=none") {
		t.Errorf("an unmarked check must carry no gate context: %q", out)
	}
}
