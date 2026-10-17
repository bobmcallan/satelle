package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// readyRepo scaffolds a repo whose readiness step declares no mark, with a story
// still in backlog and one that has already started.
func readyRepo(t *testing.T) (backlog, started string) {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "readied", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
propose = true
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["readied"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now().UTC()); err != nil {
		t.Fatalf("doc sync: %v", err)
	}
	mk := func(status string) string {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: "x", Body: "b", AcceptanceCriteria: "1. ok",
			Status: status, Category: "fix",
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return it.ID
	}
	return mk("backlog"), mk("in_progress")
}

// sty_a32bd6a9 AC9: one result line per story, a refusal names the reason, and
// the command exits non-zero when any story was refused or not accepted.
func TestStoryReadyPrintsOneLinePerStoryAndExitsNonZero(t *testing.T) {
	backlog, started := readyRepo(t)
	stdout, _, err := runRootSplit(t, "", "story", "ready", backlog, started)
	if err == nil {
		t.Fatalf("a refused story must exit non-zero; stdout:\n%s", stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per story, got %d:\n%s", len(lines), stdout)
	}
	if !strings.HasPrefix(lines[0], backlog+" refused: ") || !strings.Contains(lines[0], "mark") {
		t.Errorf("a step with no mark is refused naming the key: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], started+" refused: ") || !strings.Contains(lines[1], "start status") {
		t.Errorf("a started story is refused naming the start status: %q", lines[1])
	}
}

func TestStoryReadyNeedsAnID(t *testing.T) {
	if _, _, err := runRootSplit(t, "", "story", "ready"); err == nil {
		t.Fatal("story ready with no id must fail")
	}
}
