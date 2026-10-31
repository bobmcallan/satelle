package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// taskOf files a task work item under parent in the current machine's store.
func taskOf(t *testing.T, parent string) string {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	it, err := db.Stories.Create(context.Background(), workitem.CreateInput{
		Kind: workitem.KindTask, Title: "result for " + parent, Body: "b", Status: "backlog", ParentID: parent,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return it.ID
}

// limitOneOn writes [engagement] limit = 1 into the machine's config.
func limitOneOn(t *testing.T, fx *holdFixture) {
	t.Helper()
	raw, err := os.ReadFile(fx.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.cfg, append(raw, []byte("\n[engagement]\nlimit = 1\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sty_9c782f0e AC8: "A task can then append a result to a story's chain from
// any session." Location A drives story S. A session at location B that never
// engaged S appends a task's result to S's chain: nothing is held, so nothing
// refuses it but a stale base. Under [engagement] limit = 1 at B a second story
// is refused naming S — counted from S's chain, which B only pulled (AC4).
func TestTaskAppendsFromAnySession(t *testing.T) {
	ts, f := newFakeWorkstateServerWith(t, noHoldRoutes(t))
	a := workingMachine(t, ts, f, "sess_machine_a")
	b := workingMachine(t, ts, f, "sess_task_b")

	a.use(t)
	id := createStoryID(t, "story a task reports into")
	if out, err := engageStory(t, id); err != nil {
		t.Fatalf("A engage: %v\n%s", err, out)
	}
	a.push(t)

	b.use(t)
	b.pull(t)
	limitOneOn(t, b)
	task := taskOf(t, id)
	if out, err := runRoot(t, "story", "log", id, "--kind", "task_result", "--data", "task="+task, "--data", "result=green"); err != nil {
		t.Fatalf("B's task must append its result to S's chain: %v\n%s", err, out)
	}
	b.push(t)
	if !f.chain.carries("task_result") {
		t.Fatal("S's chain on the server does not carry the task's result")
	}

	// The count is S's chain fold: a second story at B is refused naming S.
	other := createStoryID(t, "a second story at B")
	out, err := engageStory(t, other)
	if err == nil || !strings.Contains(err.Error()+out, id) || !strings.Contains(err.Error()+out, "[engagement] limit = 1") {
		t.Fatalf("under limit = 1 a second story at B must be refused naming %s: %v\n%s", id, err, out)
	}

	// A moves S on; B's next result builds on the base it last saw and is refused
	// by the chain's base check alone, naming A's presence.
	a.use(t)
	a.pull(t)
	if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", "comment", "--actor", "executor", "--body", "A moves on"); err != nil {
		t.Fatalf("A append: %v\n%s", err, out)
	}
	a.push(t)

	b.use(t)
	if out, err := runRoot(t, "story", "log", id, "--kind", "task_result", "--data", "task="+task, "--data", "result=again"); err != nil {
		t.Fatalf("recording a result locally is never refused: %v\n%s", err, out)
	}
	out, err = runRoot(t, "sync", "workstate", "push", "--server", ts.URL)
	if err == nil {
		t.Fatalf("B's result on a stale base must be refused:\n%s", out)
	}
	for _, want := range []string{"the story " + id, "moved since", "working on at " + a.loc} {
		if !strings.Contains(out, want) {
			t.Errorf("B's refusal lacks %q:\n%s", want, out)
		}
	}
}
