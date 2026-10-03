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

// waveRepo scaffolds a repo whose epic-parent route declares schedule, with an
// epic of three children: two free and one waiting on an unfinished dependency.
func waveRepo(t *testing.T, schedule string) (epic string, free []string, waiting, dep string) {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
schedule = "`+schedule+`"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

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
	mk := func(category string, status string, tags ...string) string {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: "x", Body: "b", AcceptanceCriteria: "1. ok",
			Status: status, Category: category, Tags: append([]string{"epic:w"}, tags...),
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return it.ID
	}
	epic = mk("epic-parent", "backlog")
	free = []string{mk("fix", "backlog"), mk("fix", "backlog")}
	dep = mk("fix", "backlog")
	waiting = mk("fix", "backlog", "depends-on:"+dep)
	return
}

func TestStoryWaveCommandPrintsRunnableIDs(t *testing.T) {
	epic, free, waiting, dep := waveRepo(t, "parallel")
	stdout, stderr, err := runRootSplit(t, "", "story", "wave", epic)
	if err != nil {
		t.Fatalf("story wave: %v\nstderr: %s", err, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	ids := map[string]bool{}
	for _, l := range lines {
		if !strings.HasPrefix(l, "#") {
			ids[l] = true
		}
	}
	// the free children and the dependency itself (it has no dependency) are
	// runnable; the child waiting on the unfinished dependency is not.
	for _, id := range append(append([]string{}, free...), dep) {
		if !ids[id] {
			t.Errorf("%s must be printed as runnable:\n%s", id, stdout)
		}
	}
	if ids[waiting] {
		t.Errorf("%s waits on %s and must not be printed as runnable:\n%s", waiting, dep, stdout)
	}
	if !strings.Contains(stdout, "# omitted "+waiting) || !strings.Contains(stdout, dep) {
		t.Errorf("the omitted child must be listed naming its dependency:\n%s", stdout)
	}
}

func TestStoryWaveCommandSequentialRefusalPrintsNoRunnableID(t *testing.T) {
	epic, free, _, _ := waveRepo(t, "sequential")
	stdout, stderr, err := runRootSplit(t, "", "story", "wave", epic)
	if err == nil {
		t.Fatalf("sequential with several runnable children must exit non-zero; stdout: %s", stdout)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a refusal must print no id on stdout, got %q", stdout)
	}
	msg := err.Error() + stderr
	for _, want := range append(append([]string{}, free...), "depends-on") {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal must contain %q: %s", want, msg)
		}
	}
}

func TestStoryWaveIsDocumented(t *testing.T) {
	out, err := runHelp(t, "help", "epic-wave")
	if err != nil || !strings.Contains(out, "story wave") || !strings.Contains(out, "depends-on") {
		t.Errorf("help epic-wave must document the command: err=%v\n%s", err, out)
	}
	root := NewRootCmd()
	cmd, _, ferr := root.Find([]string{"story", "wave"})
	if ferr != nil || cmd == nil || cmd.Name() != "wave" || !strings.Contains(cmd.Long, "satelle help epic-wave") {
		t.Errorf("story wave must be a registered command pointing at its help topic: %v", ferr)
	}
}
