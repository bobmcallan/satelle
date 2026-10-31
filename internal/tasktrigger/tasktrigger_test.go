package tasktrigger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The execution route: backlog → in_progress (performed by the executor) → done,
// with no gates and no park state.
const routeDone = `["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled" }
`

const routeStep = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// env is the verb layer wired to a real store, with one task whose file declares
// the triggers under test.
type env struct {
	t    *testing.T
	db   *store.DB
	task string
}

func newEnv(t *testing.T, triggers string) *env {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	restore, _ := verb.SnapshotWiring()
	t.Cleanup(restore)
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"done": routeDone, "step": testutil.UngatedStep(routeStep)} {
		doc := "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" + body
		if err := os.WriteFile(filepath.Join(wf, name+".toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wf}, time.Now()); err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	verb.SetTaskDir(filepath.Join(dir, "tasks"))

	// The task is configuration: its triggers exist only in this file.
	taskFile := "---\nid: tsk_release\ntype: task\nstatus: backlog\ninputs_decl: trigger:string, trigger_source:string\ntriggers: " + triggers + "\n---\n\n# Release\n\nMerge and release; verify.\n"
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks", "tsk_release.md"), []byte(taskFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := verb.SyncTasks(context.Background(), db.Stories, time.Now()); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, db: db, task: "tsk_release"}
}

func (e *env) executions() []workitem.Item {
	e.t.Helper()
	its, err := e.db.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindExecution, ParentID: e.task})
	if err != nil {
		e.t.Fatal(err)
	}
	return its
}

func (e *env) storyAt(status string) {
	e.t.Helper()
	if _, err := e.db.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: "s", Status: status}, time.Now()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) evaluate(now time.Time, perf drive.Performers) []Fired {
	e.t.Helper()
	fired, err := Evaluate(context.Background(), now, drive.Options{Performers: perf})
	if err != nil {
		e.t.Fatal(err)
	}
	return fired
}

// executor performs the working step with step, or does nothing when step is nil.
type executor struct{ step func() error }

func (p executor) StepPerformer(_ context.Context, _ workitem.Item, step string) (verb.PerformerResolution, error) {
	if step == "in_progress" {
		return verb.PerformerResolution{Kind: verb.PerformerExecutor}, nil
	}
	return verb.PerformerResolution{Kind: verb.PerformerNone}, nil
}

func (p executor) ExecutorBinding() (config.AgentBinding, bool) {
	return config.AgentBinding{Command: "fake-cli -p {system}"}, true
}

func (p executor) PerformExecutorStep(context.Context, workitem.Item, string, string) (verb.DispatchResult, error) {
	res := verb.DispatchResult{Dispatched: true, Agent: "executor", Command: "fake-cli", Skill: "s"}
	if p.step != nil {
		return res, p.step()
	}
	return res, nil
}

var crash = executor{step: func() error { return errors.New("the agent crashed") }}

// A schedule fires once, not again within its interval, and again once the
// interval has passed since its last execution; each run is driven to the end of
// the task's route locally.
func TestScheduleTriggerFiresOncePerInterval(t *testing.T) {
	e := newEnv(t, "nightly=schedule:1h")
	first := e.evaluate(time.Now(), executor{})
	if len(first) != 1 || first[0].Err != nil || !first[0].Drive.Terminal {
		t.Fatalf("first pass = %+v, want one firing driven to its terminal step", first)
	}
	t0 := e.executions()[0].CreatedAt
	if again := e.evaluate(t0.Add(30*time.Minute), executor{}); len(again) != 0 {
		t.Errorf("half an interval later fired %+v, want nothing", again)
	}
	if later := e.evaluate(t0.Add(time.Hour), executor{}); len(later) != 1 || later[0].Err != nil {
		t.Errorf("an interval later fired %+v, want one firing", later)
	}
	if n := len(e.executions()); n != 2 {
		t.Errorf("executions = %d, want 2", n)
	}
}

// A count fires one execution when its threshold is reached, none while that run
// has not concluded, and again once it has and the condition still holds.
func TestCountTriggerFiresAtThresholdAndWaitsForTheRunToConclude(t *testing.T) {
	e := newEnv(t, "release=count:awaiting_merge>=3")
	e.storyAt("awaiting_merge")
	e.storyAt("awaiting_merge")
	e.storyAt("in_progress")
	if fired := e.evaluate(time.Now(), executor{}); len(fired) != 0 {
		t.Fatalf("two stories at the status fired %+v, want nothing", fired)
	}

	e.storyAt("awaiting_merge")
	fired := e.evaluate(time.Now(), crash)
	var stopped *drive.Stopped
	if len(fired) != 1 || fired[0].Execution == "" || !errors.As(fired[0].Err, &stopped) {
		t.Fatalf("three stories fired %+v, want one execution whose drive stopped short", fired)
	}
	if again := e.evaluate(time.Now(), executor{}); len(again) != 0 {
		t.Errorf("with the run not concluded fired %+v, want nothing", again)
	}
	if n := len(e.executions()); n != 1 {
		t.Fatalf("executions = %d, want 1", n)
	}

	if _, err := e.db.Stories.SetStatus(context.Background(), fired[0].Execution, "done", time.Now()); err != nil {
		t.Fatal(err)
	}
	if after := e.evaluate(time.Now(), executor{}); len(after) != 1 || after[0].Err != nil || !after[0].Drive.Terminal {
		t.Errorf("after the run concluded fired %+v, want one more firing driven to its end", after)
	}
}

// The execution a firing makes records the trigger and its source: as the inputs
// the task declares, as its tag, and in its body.
func TestFiredExecutionRecordsTheTriggerAndItsSource(t *testing.T) {
	e := newEnv(t, "release=count:awaiting_merge>=1")
	e.storyAt("awaiting_merge")
	fired := e.evaluate(time.Now(), executor{})
	if len(fired) != 1 || fired[0].Execution == "" {
		t.Fatalf("fired = %+v, want one execution", fired)
	}
	var exec workitem.Item
	if err := verb.DispatchJSON(context.Background(), "execution-get", map[string]any{"id": fired[0].Execution}, &exec); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"trigger": "release", "trigger_source": "count"}; !reflect.DeepEqual(exec.InputValues, want) {
		t.Errorf("inputs = %v, want %v", exec.InputValues, want)
	}
	if exec.ParentID != e.task || !containsString(exec.Tags, "trigger:tsk_release:release") {
		t.Errorf("parent %q tags %v, want under %s tagged trigger:tsk_release:release", exec.ParentID, exec.Tags, e.task)
	}
	if !strings.Contains(exec.Body, `"release"`) || !strings.Contains(exec.Body, "source count") {
		t.Errorf("body = %q, want it to name the trigger and its source", exec.Body)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
