package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// driveFixture is a repo whose route is
//
//	backlog → plan (planner, propose, gated) → in_progress (coder) → integration (executor) → done
//
// with every performer a shell script that logs what it was handed and every
// gate a functional check whose verdict a counter file scripts. A drive over it
// exercises the real engine, the real verbs and the real ledger; only the
// agents behind the bindings are stubs.
type driveFixture struct {
	t    *testing.T
	repo string
	dir  string // scripts, logs and the gate counter
}

type driveOpts struct {
	// gateRejects is how many presentations of the plan edge the gate rejects
	// before it accepts (999 = always).
	gateRejects int
	// rejectBudget is the plan step's reject_budget (0 = undeclared).
	rejectBudget int
	// executorCommand / coderCommand override the headless bindings ("" = a
	// logging stub script; "in-loop" = the driving session performs).
	executorCommand string
	coderCommand    string
	// noPark drops the route's park state.
	noPark bool
}

const driveIntegrateSkill = `---
name: drv-integrate
type: skill
description: the integration step's executor rubric for the drive tests
---

INTEGRATE-RUBRIC-MARKER: integrate the slice.
`

func (f *driveFixture) stub(name string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, name)
	log := filepath.Join(f.dir, name+".log")
	script := "#!/bin/sh\n{ echo \"=== ARGV: $*\"; cat; echo; } >> " + log + "\necho performed\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// log is everything the named stub was handed, one "=== ARGV" block per run.
func (f *driveFixture) log(name string) []string {
	b, err := os.ReadFile(filepath.Join(f.dir, name+".log"))
	if err != nil {
		return nil
	}
	parts := strings.Split(string(b), "=== ARGV:")
	return parts[1:]
}

func newDriveFixture(t *testing.T, o driveOpts) *driveFixture {
	t.Helper()
	repo := tempRepo(t)
	t.Chdir(repo)
	f := &driveFixture{t: t, repo: repo, dir: t.TempDir()}
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	park := "park = { state = \"blocked\" }\n"
	if o.noPark {
		park = ""
	}
	budget := ""
	if o.rejectBudget > 0 {
		budget = fmt.Sprintf("reject_budget = %d\n", o.rejectBudget)
	}
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "readied", "coded", "integrated", "closed"]
`+park+`cancel = { state = "cancelled" }
`,
		`[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["drv-plan"]
propose = true
reviewers = ["drv-gate"]
`+budget+`requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
requires = ["readied"]

[integrated]
status = "integration"
agent = "executor"
skills = ["drv-integrate"]
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]
`)
	skills := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(f.dir, "gate-count")
	gate := "---\nname: drv-gate\ntype: skill\ndescription: scripted gate for the drive tests\n---\n\n```check\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + counter + "\n" +
		fmt.Sprintf("if [ \"$n\" -le %d ]; then echo \"NOTE-FIX-THE-PLAN (presentation $n)\"; exit 1; fi\nexit 0\n```\n", o.gateRejects)
	plan := "---\nname: drv-plan\ntype: skill\ndescription: the plan step's rubric for the drive tests\n---\n\nPlan the slice.\n"
	for name, body := range map[string]string{"drv-gate.md": gate, "drv-integrate.md": driveIntegrateSkill, "drv-plan.md": plan} {
		if err := os.WriteFile(filepath.Join(skills, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tools := "tools = \"Read,Grep,Glob,Edit,Write,Bash(satelle:*)\"\n"
	exec := o.executorCommand
	if exec == "" {
		exec = f.stub("fake-executor") + " -p {system}"
	}
	coder := o.coderCommand
	if coder == "" {
		coder = f.stub("fake-coder") + " -p {system}"
	}
	agents := "[executor]\nrole = \"agent\"\ncommand = \"" + exec + "\"\n" + tools + "\n" +
		"[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[planner]\nrole = \"agent\"\ncommand = \"" + f.stub("fake-planner") + " -p {system}\"\n" + tools + "\n" +
		"[coder]\nrole = \"agent\"\ncommand = \"" + coder + "\"\n" + tools
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	return f
}

func (f *driveFixture) create() string {
	f.t.Helper()
	out, err := runRoot(f.t, "story", "create", "--title", "drive me", "--body", "walk the route",
		"--acceptance", "1. the route is walked", "--category", "chore")
	if err != nil {
		f.t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		f.t.Fatalf("parse create: %v\n%s", err, out)
	}
	return created["id"].(string)
}

func (f *driveFixture) rows(id string) []ledger.Entry {
	f.t.Helper()
	db, err := store.Open(runtimeDBPath(f.t))
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	es, err := db.Ledger.ListByStory(context.Background(), id, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return es
}

// edges is the story's status_transition rows as "from>to", in order.
func (f *driveFixture) edges(id string) []string {
	var out []string
	for _, e := range f.rows(id) {
		if e.Kind != ledger.KindStatusTransition {
			continue
		}
		var p struct{ From, To string }
		_ = json.Unmarshal(e.Payload, &p)
		out = append(out, p.From+">"+p.To)
	}
	return out
}

// AC1: the drive walks every edge of the spine through story-set and ends done,
// leaving the same transition rows a hand-driven `story set` walk leaves.
func TestStoryDriveWalksTheSpineToDone(t *testing.T) {
	f := newDriveFixture(t, driveOpts{})
	driven := f.create()
	out, err := runRoot(t, "story", "drive", driven)
	if err != nil {
		t.Fatalf("drive: %v\n%s", err, out)
	}
	if got := storyStatus(t, driven); got != "done" {
		t.Fatalf("status = %q, want done\n%s", got, out)
	}
	if !strings.Contains(out, driven+" done (terminal)") {
		t.Errorf("the drive must end on its summary line, got:\n%s", out)
	}

	manual := f.create()
	for _, to := range []string{"plan", "in_progress", "integration", "done"} {
		if o, err := runRoot(t, "story", "set", manual, "--status", to); err != nil {
			t.Fatalf("manual set %s: %v\n%s", to, err, o)
		}
	}
	got, want := f.edges(driven), f.edges(manual)
	if len(got) != 4 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("a drive records one transition row per edge, as a hand walk does:\n drive:  %v\n manual: %v", got, want)
	}
	// Every performer ran: the planner and the coder on entering their steps and
	// the executor once the story was in integration.
	for _, name := range []string{"fake-planner", "fake-coder", "fake-executor"} {
		if n := len(f.log(name)); n < 1 {
			t.Errorf("%s never ran", name)
		}
	}
}

// AC2, case 1: an executor-allocated step while [executor] is in-loop is refused
// before any transition, naming the step and the agents.toml key.
func TestStoryDriveRefusesAnInLoopExecutor(t *testing.T) {
	f := newDriveFixture(t, driveOpts{executorCommand: "in-loop"})
	id := f.create()
	before := len(f.rows(id))
	out, err := runRoot(t, "story", "drive", id)
	if err == nil {
		t.Fatalf("an in-loop executor must be refused:\n%s", out)
	}
	for _, want := range []string{`step "integration"`, "[executor] command"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
	if ExitCodeOf(err) != 1 {
		t.Errorf("a refusal exits 1, got %d", ExitCodeOf(err))
	}
	if got := storyStatus(t, id); got != "backlog" || len(f.edges(id)) != 0 {
		t.Errorf("a refused drive makes no transition: status %q, edges %v", got, f.edges(id))
	}
	if after := f.rows(id); len(after) != before {
		t.Errorf("a refusal before the first move writes nothing: %d ledger rows became %d", before, len(after))
	}
}

// AC2, case 2: a step whose named agent is bound in-loop is refused the same way.
func TestStoryDriveRefusesANamedAgentBoundInLoop(t *testing.T) {
	f := newDriveFixture(t, driveOpts{coderCommand: "in-loop"})
	id := f.create()
	before := len(f.rows(id))
	out, err := runRoot(t, "story", "drive", id)
	if err == nil {
		t.Fatalf("an in-loop named agent must be refused:\n%s", out)
	}
	for _, want := range []string{`step "in_progress"`, `agent "coder"`, "[coder]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
	if ExitCodeOf(err) != 1 || storyStatus(t, id) != "backlog" {
		t.Errorf("a refusal exits 1 and moves nothing: %d, %q", ExitCodeOf(err), storyStatus(t, id))
	}
	if after := f.rows(id); len(after) != before {
		t.Errorf("a refusal before the first move writes nothing: %d ledger rows became %d", before, len(after))
	}
}

// AC2, case 3: on entering an executor-allocated step the headless executor binding
// runs once, with the step's executor skill, before the next edge is presented.
func TestStoryDriveRunsTheExecutorBindingOnEnteringItsStep(t *testing.T) {
	f := newDriveFixture(t, driveOpts{})
	id := f.create()
	if out, err := runRoot(t, "story", "drive", id); err != nil {
		t.Fatalf("drive: %v\n%s", err, out)
	}
	runs := f.log("fake-executor")
	if len(runs) != 1 {
		t.Fatalf("the executor binding must run exactly once, ran %d times", len(runs))
	}
	if !strings.Contains(runs[0], "INTEGRATE-RUBRIC-MARKER") {
		t.Errorf("the step's executor skill must reach the binding as its rubric:\n%s", runs[0])
	}
	// Order on the ledger: entered integration, then performed it, then the exit edge.
	var order []string
	for _, e := range f.rows(id) {
		switch {
		case e.Kind == ledger.KindStatusTransition && strings.Contains(string(e.Payload), `"to":"integration"`):
			order = append(order, "entered")
		case e.Kind == ledger.KindAgentInvocation && strings.Contains(e.Body, `performed step "integration"`):
			order = append(order, "performed")
		case e.Kind == ledger.KindStatusTransition && strings.Contains(string(e.Payload), `"to":"done"`):
			order = append(order, "closed")
		}
	}
	if strings.Join(order, ",") != "entered,performed,closed" {
		t.Errorf("ledger order = %v, want entered, performed, closed", order)
	}
}

// AC3: when the gate rejects, the findings reach the performer the engine names
// for the step as a message BEFORE the edge is presented again, and the re-dispatch
// carries them in its payload.
func TestStoryDriveRelaysARejectionToThePerformerBeforeRepresenting(t *testing.T) {
	f := newDriveFixture(t, driveOpts{gateRejects: 1})
	id := f.create()
	if out, err := runRoot(t, "story", "drive", id); err != nil {
		t.Fatalf("drive: %v\n%s", err, out)
	}
	runs := f.log("fake-planner")
	if len(runs) != 2 {
		t.Fatalf("the planner is dispatched on each presentation of the plan edge, ran %d times", len(runs))
	}
	if strings.Contains(runs[0], "NOTE-FIX-THE-PLAN") {
		t.Errorf("the first dispatch precedes any rejection, yet saw the notes:\n%s", runs[0])
	}
	if !strings.Contains(runs[1], "NOTE-FIX-THE-PLAN") || !strings.Contains(runs[1], `"messages"`) {
		t.Errorf("the re-dispatch payload must carry the relayed notes as messages:\n%s", runs[1])
	}
	var order []string
	for _, e := range f.rows(id) {
		switch {
		case e.Kind == ledger.KindReviewReject:
			order = append(order, "reject")
		case e.Kind == "agent_message" && strings.Contains(e.Body, "NOTE-FIX-THE-PLAN") && strings.Contains(string(e.Payload), `"to":"planner"`):
			order = append(order, "message")
		case e.Kind == ledger.KindAgentInvocation && strings.Contains(e.Body, `dispatched step "plan"`):
			order = append(order, "dispatch")
		}
	}
	// The rows of one presentation share the transition's timestamp, so only the
	// order ACROSS presentations is meaningful: the first presentation's dispatch
	// and reject, then the message, then the re-dispatch.
	if len(order) != 4 || order[2] != "message" || order[3] != "dispatch" ||
		order[0]+order[1] != "dispatchreject" && order[0]+order[1] != "rejectdispatch" {
		t.Errorf("order = %v, want {dispatch, reject}, message, dispatch (the message precedes the re-dispatch)", order)
	}
}

// AC3 + AC4: a reviewer that always rejects ends the story parked, with the last
// objection recorded on its timeline, and the process exits 3 after printing the
// status and the reason.
func TestStoryDriveParksOnASpentRejectBudgetAndExitsThree(t *testing.T) {
	f := newDriveFixture(t, driveOpts{gateRejects: 999, rejectBudget: 2})
	id := f.create()
	out, err := runRoot(t, "story", "drive", id)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || ExitCodeOf(err) != 3 {
		t.Fatalf("a parked drive must return an ExitError{3}, got %v\n%s", err, out)
	}
	if got := storyStatus(t, id); got != "blocked" {
		t.Fatalf("status = %q, want blocked", got)
	}
	for _, want := range []string{id + " parked at blocked", "NOTE-FIX-THE-PLAN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must carry %q:\n%s", want, out)
		}
	}
	var reason string
	for _, e := range f.rows(id) {
		if e.Kind == ledger.KindComment && strings.Contains(e.Body, "NOTE-FIX-THE-PLAN") {
			reason = e.Body
		}
	}
	if reason == "" {
		t.Errorf("the objection must be on the timeline as the park reason; rows: %v", f.edges(id))
	}
}

// AC5: a workflow that offers no park state to hold the question cannot park the
// story, so the drive records the stop as a drive-stopped row and exits 1, printing
// where and why it stopped.
func TestStoryDriveWithNoParkStateStops(t *testing.T) {
	f := newDriveFixture(t, driveOpts{gateRejects: 999, rejectBudget: 1, noPark: true})
	id := f.create()
	out, err := runRoot(t, "story", "drive", id)
	if err == nil {
		t.Fatalf("a drive that must park but cannot has to stop:\n%s", out)
	}
	line := id + " stopped at backlog: step plan, performer planner:"
	if ExitCodeOf(err) != 1 || !strings.Contains(out, line) || !strings.Contains(err.Error(), line) {
		t.Errorf("want exit 1 and %q on the output, got %d: %v\n%s", line, ExitCodeOf(err), err, out)
	}
	if got := storyStatus(t, id); got == "blocked" {
		t.Errorf("no park state exists, yet the story is %q", got)
	}
	var stops int
	for _, e := range f.rows(id) {
		if e.Kind == ledger.KindDriveStopped {
			stops++
			if !strings.Contains(e.Body, "NOTE-FIX-THE-PLAN") || !strings.Contains(string(e.Payload), "no park state") {
				t.Errorf("the stop row must carry the objection and why it could not park: %q %s", e.Body, e.Payload)
			}
		}
	}
	if stops != 1 {
		t.Errorf("want exactly one drive-stopped row, got %d", stops)
	}
}

// AC4: the process exit code is read off the ExitError chain.
func TestExitCodeOf(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errors.New("plain"), 1},
		{&ExitError{Code: 3, Err: errors.New("parked")}, 3},
		{fmt.Errorf("wrapped: %w", &ExitError{Code: 3}), 3},
		{&gateExitError{msg: "x"}, 1},
		{&gateExitError{msg: "x", code: 3}, 3},
	} {
		if got := ExitCodeOf(tc.err); got != tc.want {
			t.Errorf("ExitCodeOf(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

// AC5: an agent caller's drive is handed off as a whole — the whole walk runs in
// one detached copy, and the caller is answered with its outcome.
func TestStoryDriveHandsOffForAnAgentCaller(t *testing.T) {
	f := newDriveFixture(t, driveOpts{})
	id := f.create()
	args := []string{"story", "drive", id}
	useGateHandOff(t, "120s", args)
	out, err := runRoot(t, args...)
	if err != nil && !errors.Is(err, errGateHandedOff) {
		t.Fatalf("drive: %v\n%s", err, out)
	}
	if got := storyStatus(t, id); got != "done" {
		t.Fatalf("the detached drive must walk the story to done, status %q\n%s", got, out)
	}
	store := gateStoreForTest(t)
	entries, _ := os.ReadDir(store.Dir())
	if len(entries) != 1 {
		t.Fatalf("the whole drive is one handed-off run, got %d handles", len(entries))
	}
	if m, _ := store.Meta(entries[0].Name()); m.Verb != "story-drive" || m.Story != id {
		t.Fatalf("handle = %+v", m)
	}
}

func TestStoryDriveNeedsAnID(t *testing.T) {
	_ = tempRepo(t)
	if _, err := runRoot(t, "story", "drive"); err == nil {
		t.Fatal("story drive with no id must fail")
	}
}

// AC7: the command's help states the exit codes and the in-loop refusal beside
// the command and points at the topic that holds the rest; the topic holds the
// full contract, so neither is the only place it is written.
func TestStoryDriveHelpDocumentsTheContract(t *testing.T) {
	long := storyDriveCommand().Long
	for _, want := range []string{"0 terminal, 3 parked, 1 refused", "in-loop", "[executor]", "parks", "satelle help agent-dispatch"} {
		if !strings.Contains(long, want) {
			t.Errorf("story drive --help must state %q", want)
		}
	}
	topic, ok := help.Get("agent-dispatch")
	if !ok {
		t.Fatal("no agent-dispatch help topic")
	}
	for _, want := range []string{"satelle story drive", "StepPerformer", "Exit status", "reject_budget", "no park state"} {
		if !strings.Contains(topic.Body, want) {
			t.Errorf("satelle help agent-dispatch must document the drive: missing %q", want)
		}
	}
}
