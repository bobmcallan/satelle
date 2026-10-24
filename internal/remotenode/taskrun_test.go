package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// taskEnv is the verb layer wired to a real store over a fixture route that has a
// park state, with one task that declares story_id (required) and trigger.
type taskEnv struct {
	t    *testing.T
	db   *store.DB
	task workitem.Item
}

// taskEnvDone routes an execution through the wildcard list, which has no park
// state, as the shipped execution route has none; taskEnvParkable adds an
// execution list that has one.
const taskEnvDone = `["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled" }
`

const taskEnvParkable = `
[execution]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`

const taskEnvStep = `[raised]
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

func newTaskEnv(t *testing.T, parkable bool) *taskEnv {
	t.Helper()
	// The run is a fresh machine's: nothing of the developer's home is read.
	t.Setenv("HOME", t.TempDir())
	done := taskEnvDone
	if parkable {
		done += taskEnvParkable
	}
	t.Setenv("SATELLE_HOME", t.TempDir())
	restore, _ := verb.SnapshotWiring()
	t.Cleanup(restore)
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"done": done, "step": testutil.UngatedStep(taskEnvStep)} {
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
	verb.SetLeaseStore(db.Leases)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	verb.SetTaskDir(filepath.Join(dir, "tasks"))

	taskFile := "---\nid: tsk_nightly-sweep\ntype: task\nstatus: backlog\ninputs_decl: story_id:string:required, trigger:string\n---\n\n# Nightly sweep\n\nSweep; verify.\n"
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks", "tsk_nightly-sweep.md"), []byte(taskFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := verb.SyncTasks(context.Background(), db.Stories, time.Now()); err != nil {
		t.Fatal(err)
	}
	task, err := db.Stories.Get(context.Background(), "tsk_nightly-sweep")
	if err != nil {
		t.Fatal(err)
	}
	return &taskEnv{t: t, db: db, task: task}
}

func (e *taskEnv) executions() []workitem.Item {
	e.t.Helper()
	its, err := e.db.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindExecution, ParentID: e.task.ID})
	if err != nil {
		e.t.Fatal(err)
	}
	return its
}

// executorOnly performs the working step with step and nothing else.
type executorOnly struct {
	step func(item workitem.Item) error
}

func (p executorOnly) StepPerformer(_ context.Context, _ workitem.Item, step string) (verb.PerformerResolution, error) {
	if step == "in_progress" {
		return verb.PerformerResolution{Kind: verb.PerformerExecutor}, nil
	}
	return verb.PerformerResolution{Kind: verb.PerformerNone}, nil
}

func (p executorOnly) ExecutorBinding() (config.AgentBinding, bool) {
	return config.AgentBinding{Command: "fake-cli -p {system}"}, true
}

func (p executorOnly) PerformExecutorStep(_ context.Context, item workitem.Item, _, _ string) (verb.DispatchResult, error) {
	res := verb.DispatchResult{Dispatched: true, Agent: "executor", Command: "fake-cli", Skill: "s"}
	if p.step != nil {
		return res, p.step(item)
	}
	return res, nil
}

// statusLog records the heartbeats a run sends.
type statusLog struct {
	mu   sync.Mutex
	sent []NodeStatus
	err  error
}

func (l *statusLog) Status(_ context.Context, s NodeStatus) (StatusReply, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, s)
	return StatusReply{}, l.err
}

func (l *statusLog) all() []NodeStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]NodeStatus(nil), l.sent...)
}

func taskRun(e *taskEnv, perf drive.Performers, log *statusLog) TaskRun {
	return TaskRun{
		Def:    remotedef.Definition{Drive: remotedef.Drive{Kind: remotedef.DriveTask, Task: "nightly-sweep"}},
		Desc:   RunDescriptor{Mode: "node", StoryID: "sty_subject", Trigger: "nightly", TriggerSource: "schedule"},
		NodeID: "nod_1", Client: log, Drive: drive.Options{Performers: perf}, ResendEvery: time.Hour,
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

func lines(sent []NodeStatus) []string {
	var out []string
	for _, s := range sent {
		out = append(out, s.RunState+"/"+s.Step+"/"+s.Outcome)
	}
	return out
}

// The wake makes one execution whose inputs are the descriptor fields the task
// declares, and a second run for the same node id finds that execution rather than
// making another.
func TestRunTaskMakesOneExecutionForTheNodeAndMapsDeclaredInputs(t *testing.T) {
	e := newTaskEnv(t, false)
	log := &statusLog{}
	res, err := RunTask(context.Background(), taskRun(e, executorOnly{}, log))
	if err != nil {
		t.Fatal(err)
	}
	execs := e.executions()
	if len(execs) != 1 || execs[0].ID != res.Execution {
		t.Fatalf("executions = %+v, want exactly the one RunTask returned (%s)", execs, res.Execution)
	}
	if want := map[string]any{"story_id": "sty_subject", "trigger": "nightly"}; !reflect.DeepEqual(execs[0].InputValues, want) {
		t.Errorf("inputs = %v, want %v (trigger_source is not declared by the task)", execs[0].InputValues, want)
	}
	if !containsString(execs[0].Tags, ExecutionTag+"nod_1") {
		t.Errorf("tags = %v, want the node's tag", execs[0].Tags)
	}

	again, err := RunTask(context.Background(), taskRun(e, executorOnly{}, &statusLog{}))
	if err != nil {
		t.Fatal(err)
	}
	if again.Execution != res.Execution || len(e.executions()) != 1 {
		t.Errorf("a second run for the same node made %s (executions %d), want the same execution %s", again.Execution, len(e.executions()), res.Execution)
	}

	other := taskRun(e, executorOnly{}, &statusLog{})
	other.NodeID = "nod_2"
	if third, err := RunTask(context.Background(), other); err != nil || third.Execution == res.Execution {
		t.Errorf("another node's wake = %+v, %v, want its own execution", third, err)
	}
}

// A route that reaches its end with no outcome recorded is performed; the status
// sequence is queued, running at each step, concluded.
func TestRunTaskWithNoRecordedOutcomeReportsPerformed(t *testing.T) {
	e := newTaskEnv(t, false)
	log := &statusLog{}
	res, err := RunTask(context.Background(), taskRun(e, executorOnly{}, log))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"queued//", "running/in_progress/", "running/done/", "concluded//performed"}
	if got := lines(log.all()); !reflect.DeepEqual(got, want) {
		t.Errorf("heartbeats = %v, want %v", got, want)
	}
	if res.Outcome != verb.OutcomePerformed || !res.Drive.Terminal {
		t.Errorf("result = %+v", res)
	}
	for _, s := range log.all() {
		if s.State != StateRunning || s.StoryID != "sty_subject" {
			t.Errorf("heartbeat %+v: want the running state for the subject", s)
		}
	}
}

// A performer that records no-op concludes the execution without error and the
// node reports no-op, read from the run-output doc.
func TestRunTaskReportsTheOutcomeThePerformerRecorded(t *testing.T) {
	e := newTaskEnv(t, false)
	log := &statusLog{}
	perf := executorOnly{step: func(item workitem.Item) error {
		req, _ := json.Marshal(map[string]any{"id": item.ID, "output": "nothing to do", "outcome": "no-op"})
		_, err := verb.Dispatch(context.Background(), "execution-record", req)
		return err
	}}
	res, err := RunTask(context.Background(), taskRun(e, perf, log))
	if err != nil || res.Outcome != verb.OutcomeNoOp || !res.Drive.Terminal {
		t.Fatalf("result = %+v, %v, want terminal with outcome no-op", res, err)
	}
	sent := log.all()
	if last := sent[len(sent)-1]; last.RunState != RunConcluded || last.Outcome != verb.OutcomeNoOp {
		t.Errorf("last heartbeat = %+v, want concluded/no-op", last)
	}
}

// A run whose performer fails ends short of its route and is concluded as
// rejected: parked where the route has a park state, an error where it has none
// (the shipped execution route has none). A wake whose required input the
// descriptor does not carry makes no execution and is concluded as rejected too.
func TestRunTaskThatDoesNotFinishConcludesRejected(t *testing.T) {
	crash := executorOnly{step: func(workitem.Item) error { return errors.New("the agent crashed") }}

	e := newTaskEnv(t, true)
	log := &statusLog{}
	res, err := RunTask(context.Background(), taskRun(e, crash, log))
	if err != nil || !res.Drive.Parked || res.Outcome != verb.OutcomeReject {
		t.Fatalf("result = %+v, %v, want a park reported as reject", res, err)
	}
	if sent := log.all(); sent[len(sent)-1].RunState != RunConcluded || sent[len(sent)-1].Outcome != verb.OutcomeReject {
		t.Errorf("last heartbeat = %+v, want concluded/reject", sent[len(sent)-1])
	}

	eNoPark := newTaskEnv(t, false)
	logNoPark := &statusLog{}
	res, err = RunTask(context.Background(), taskRun(eNoPark, crash, logNoPark))
	var stopped *drive.Stopped
	if !errors.As(err, &stopped) || res.Outcome != verb.OutcomeReject {
		t.Fatalf("result = %+v, %v, want a drive-stopped error reported as reject", res, err)
	}
	if sent := logNoPark.all(); sent[len(sent)-1].RunState != RunConcluded || sent[len(sent)-1].Outcome != verb.OutcomeReject {
		t.Errorf("last heartbeat = %+v, want concluded/reject", sent[len(sent)-1])
	}

	e2 := newTaskEnv(t, false)
	log2 := &statusLog{}
	tr := taskRun(e2, executorOnly{}, log2)
	tr.Desc.StoryID = ""
	if _, err := RunTask(context.Background(), tr); err == nil || !strings.Contains(err.Error(), "story_id") {
		t.Fatalf("a wake with no story_id for a task that requires it = %v, want an error naming it", err)
	}
	if n := len(e2.executions()); n != 0 {
		t.Errorf("executions = %d, want none", n)
	}
	if got := lines(log2.all()); !reflect.DeepEqual(got, []string{"queued//", "concluded//reject"}) {
		t.Errorf("heartbeats = %v", got)
	}
}

// The resend interval is a third of the window the server reports it waits before
// listing the node lost, the default (itself under the server's 90s) when the
// server reports none, and a test's override over both.
func TestStatusReporterResendIntervalFollowsTheServersWindow(t *testing.T) {
	if DefaultStatusResendInterval >= 90*time.Second {
		t.Fatalf("the default %v is not under the server's 90s lost-after window", DefaultStatusResendInterval)
	}
	reply := &replyingSender{}
	for _, tc := range []struct {
		name     string
		lost     int
		override time.Duration
		want     time.Duration
	}{
		{"server window", 90, 0, 30 * time.Second},
		{"shorter window", 30, 0, 10 * time.Second},
		{"no window reported", 0, 0, DefaultStatusResendInterval},
		{"override", 90, 50 * time.Millisecond, 50 * time.Millisecond},
	} {
		reply.lost = tc.lost
		r := newStatusReporter(reply, "", tc.override, nil)
		r.put(context.Background(), NodeStatus{State: StateRunning})
		r.mu.Lock()
		got := r.every
		r.mu.Unlock()
		if got != tc.want {
			t.Errorf("%s: interval = %v, want %v", tc.name, got, tc.want)
		}
		if got >= time.Duration(max(tc.lost, 1))*time.Second && tc.lost > 0 && tc.override == 0 {
			t.Errorf("%s: interval %v is not under the reported window of %ds", tc.name, got, tc.lost)
		}
	}
}

type replyingSender struct{ lost int }

func (s *replyingSender) Status(context.Context, NodeStatus) (StatusReply, error) {
	return StatusReply{LostAfterSeconds: s.lost}, nil
}

// A heartbeat the server refuses does not fail the run.
func TestRunTaskSurvivesARefusedHeartbeat(t *testing.T) {
	e := newTaskEnv(t, false)
	log := &statusLog{err: errors.New("status: 503")}
	res, err := RunTask(context.Background(), taskRun(e, executorOnly{}, log))
	if err != nil || res.Outcome != verb.OutcomePerformed {
		t.Errorf("result = %+v, %v, want the run unaffected", res, err)
	}
}
