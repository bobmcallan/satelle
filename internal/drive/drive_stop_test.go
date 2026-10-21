package drive_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/drive"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A drive never ends short of its route's exit without a record (sty_aaafe4e2):
// after its first presentation every other end parks the story with a reason
// naming the step, the performer and what was missing, or — where it cannot park —
// writes a drive-stopped row.

const question = "Should I use X or Y?"

// routeNoPark is routeDone without a park state.
const routeNoPark = `["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled" }
`

// namedRoute performs in_progress with the named agent coder.
const namedRoute = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// reworkRoute is namedRoute with a declared rework relay on in_progress.
const reworkRoute = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
rework = { consult = "reviewer-consult", rounds = 2 }
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// lateNamedRoute performs the exit step, done, with the named agent coder.
const lateNamedRoute = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
requires = ["raised"]

[closed]
status = "done"
agent = "coder"
terminal = true
requires = ["coded"]
`

// budgetedRoute is workRoute whose exit declares a reject budget.
const budgetedRoute = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
reject_budget = 2
requires = ["coded"]
`

// stubDispatcher is the engine's named-performer dispatch, scripted per step.
type stubDispatcher struct {
	mu      sync.Mutex
	out     map[string]string
	errs    map[string]error
	rejects map[string]*verb.PerformerReject
}

func newStubDispatcher() *stubDispatcher {
	return &stubDispatcher{out: map[string]string{}, errs: map[string]error{}, rejects: map[string]*verb.PerformerReject{}}
}

func (d *stubDispatcher) DispatchExecutor(_ context.Context, _ workitem.Item, to string) (verb.DispatchResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out, ok := d.out[to]
	res := verb.DispatchResult{Dispatched: true, Agent: "coder", Command: "fake-coder", Skill: "coder", Output: out, LastMessage: verb.LastMessage(out)}
	switch {
	case d.rejects[to] != nil:
		return res, d.rejects[to]
	case d.errs[to] != nil:
		return res, d.errs[to]
	case !ok:
		return verb.DispatchResult{}, nil
	}
	return res, nil
}

// append writes a ledger row the way a mechanism outside the drive does.
func (e *env) append(id, kind, actor, body string, payload map[string]any) {
	e.t.Helper()
	_, err := verb.Dispatch(context.Background(), "ledger-append", mustJSON(map[string]any{
		"story_id": id, "kind": kind, "actor": actor, "body": body, "payload": payload,
	}))
	if err != nil {
		e.t.Fatalf("ledger-append %s: %v", kind, err)
	}
}

// otherInvocations appends a reviewer's and a summariser's agent_invocation row,
// which carry no last_message, so a reader of the newest row alone is wrong.
func (e *env) otherInvocations(id string) {
	e.t.Helper()
	e.append(id, ledger.KindAgentInvocation, "reviewer", "reviewed", map[string]any{"from": "a", "to": "b", "skill": "stub-gate", "tokens_in": 5})
	e.append(id, ledger.KindAgentInvocation, "reviewer", "summarised", map[string]any{"from": "a", "to": "b", "summary": true, "tokens_in": 3})
}

func payloadOf(t *testing.T, row ledger.Entry) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		t.Fatalf("row %s payload: %v (%s)", row.Kind, err, row.Payload)
	}
	return p
}

// driveRows are the park comments (payload source "drive") and the drive-stopped
// rows a drive wrote.
func (e *env) driveRows(id string) (parks, stops []ledger.Entry) {
	e.t.Helper()
	for _, row := range e.rows(id, ledger.KindComment) {
		var p struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(row.Payload, &p) == nil && p.Source == "drive" {
			parks = append(parks, row)
		}
	}
	return parks, e.rows(id, ledger.KindDriveStopped)
}

func (e *env) wantNoDriveRecords(id string) {
	e.t.Helper()
	if parks, stops := e.driveRows(id); len(parks) != 0 || len(stops) != 0 {
		e.t.Errorf("a drive that never began, or ended terminal, records no stop: %d parks, %d drive-stopped", len(parks), len(stops))
	}
}

// ledgerIDs is every row in the ledger, across all stories.
func (e *env) ledgerIDs() map[string]bool {
	e.t.Helper()
	rows, err := e.db.Ledger.ListAll(context.Background(), 0)
	if err != nil {
		e.t.Fatal(err)
	}
	ids := make(map[string]bool, len(rows))
	for _, r := range rows {
		ids[r.ID] = true
	}
	return ids
}

// wantLedgerDelta fails if the ledger holds any row that before did not: a drive
// that refuses before its first presentation writes nothing at all.
func (e *env) wantLedgerDelta(before map[string]bool) {
	e.t.Helper()
	rows, err := e.db.Ledger.ListAll(context.Background(), 0)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		if !before[r.ID] {
			e.t.Errorf("a drive that refused before its first presentation wrote a %s row (actor %s): %s", r.Kind, r.Actor, r.Body)
		}
	}
	if len(rows) != len(before) {
		e.t.Errorf("ledger holds %d rows, want the %d it held before the drive", len(rows), len(before))
	}
}

// wantStopPayload checks the keys every park comment and drive-stopped row carries.
func wantStopPayload(t *testing.T, row ledger.Entry, lastMessage string) map[string]any {
	t.Helper()
	p := payloadOf(t, row)
	for _, key := range []string{"source", "step", "performer", "reason", "last_message", "location_id"} {
		if _, ok := p[key]; !ok {
			t.Errorf("%s row payload lacks %q: %v", row.Kind, key, p)
		}
	}
	if p["source"] != "drive" || p["location_id"] != "loc-test" || p["location_label"] != "label-test" {
		t.Errorf("%s row payload = %v, want source drive and the node's location", row.Kind, p)
	}
	reason, _ := p["reason"].(string)
	if !strings.HasPrefix(reason, fmt.Sprintf("step %v, performer %v:", p["step"], p["performer"])) {
		t.Errorf("%s row reason %q does not begin with its step and performer", row.Kind, reason)
	}
	if lastMessage != "*" && p["last_message"] != lastMessage {
		t.Errorf("%s row last_message = %q, want %q", row.Kind, p["last_message"], lastMessage)
	}
	return p
}

func opts(perf drive.Performers) drive.Options {
	return drive.Options{Performers: perf, Location: "loc-test", LocationLabel: "label-test"}
}

// AC1: a presentation refused by a plain error after the first one parks the
// story, naming the step, the performer and the refusal.
func TestDriveParksOnRefusalAfterFirstMove(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	e.gater.errs["done"] = errors.New("gate backend unreachable")
	id := e.story()
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless()}
	res, err := drive.Story(context.Background(), id, opts(perf))
	if err != nil {
		t.Fatalf("a refusal after the first move parks: %v", err)
	}
	if !res.Parked || res.Status != "blocked" || e.status(id) != "blocked" {
		t.Fatalf("want parked at blocked, got %+v (stored %q)", res, e.status(id))
	}
	if !strings.HasPrefix(res.Reason, "step done, performer none:") || !strings.Contains(res.Reason, "gate backend unreachable") {
		t.Errorf("reason = %q, want the step, the performer and the refusal", res.Reason)
	}
	parks, stops := e.driveRows(id)
	if len(parks) != 1 || len(stops) != 0 {
		t.Fatalf("want one park comment and no drive-stopped row, got %d and %d", len(parks), len(stops))
	}
	wantStopPayload(t, parks[0], "*")
}

// AC8: a refusal on the very first presentation is a refusal to start: nothing
// is parked or recorded.
func TestDriveRefusalOnFirstPresentationRecordsNothing(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	e.gater.errs["in_progress"] = errors.New("gate backend unreachable")
	id := e.story()
	_, err := drive.Story(context.Background(), id, opts(&fakePerformers{}))
	if err == nil || !strings.Contains(err.Error(), "gate backend unreachable") {
		t.Fatalf("want the refusal as an error, got %v", err)
	}
	var stopped *drive.Stopped
	if errors.As(err, &stopped) || e.status(id) != "backlog" {
		t.Errorf("a refusal before the first move is plain: %v (status %q)", err, e.status(id))
	}
	e.wantNoDriveRecords(id)
}

// AC8: a drive over an id that does not exist records nothing.
func TestDriveOfAnUnknownStoryRecordsNothing(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	e.story()
	before := e.ledgerIDs()
	if _, err := drive.Story(context.Background(), "sty_nothere", opts(&fakePerformers{})); err == nil {
		t.Fatal("an unknown story must be refused")
	}
	e.wantLedgerDelta(before)
}

// AC8: a drive over an id that is not a story is refused and leaves the ledger as it was.
func TestDriveOfANonStoryRecordsNothing(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	raw, err := verb.Dispatch(context.Background(), "task-create", mustJSON(map[string]any{"title": "not a story"}))
	if err != nil {
		t.Fatalf("task-create: %v", err)
	}
	var task workitem.Item
	if err := json.Unmarshal(raw, &task); err != nil || task.ID == "" {
		t.Fatalf("task-create: %v %q", err, raw)
	}
	before := e.ledgerIDs()
	_, err = drive.Story(context.Background(), task.ID, opts(&fakePerformers{}))
	if err == nil || !strings.Contains(err.Error(), "is not a story") {
		t.Fatalf("want a refusal naming that %s is not a story, got %v", task.ID, err)
	}
	e.wantLedgerDelta(before)
}

// AC8: a story whose route cannot be resolved (the repo's done half does not
// parse, so no route governs) is refused before any presentation and leaves the
// ledger as it was.
func TestDriveWithNoRouteRecordsNothing(t *testing.T) {
	e := newEnv(t, "[\"*\"]\nobligations = [", workRoute)
	id := e.story()
	before := e.ledgerIDs()
	_, err := drive.Story(context.Background(), id, opts(&fakePerformers{}))
	if err == nil || !strings.Contains(err.Error(), "no route to walk") {
		t.Fatalf("want a refusal naming the missing route, got %v", err)
	}
	var stopped *drive.Stopped
	if errors.As(err, &stopped) || e.status(id) != "backlog" {
		t.Errorf("a refusal to start is plain: %v (status %q)", err, e.status(id))
	}
	e.wantLedgerDelta(before)
}

// AC3: a performer that exits cleanly having asked a question leaves the question
// on the park record, however many reviewer and summariser rows came after it.
func TestDriveLastMessageExecutorCleanQuestion(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	e.gater.always["done"] = true
	id := e.story()
	e.gater.onGate = func(to string) {
		if to == "done" {
			e.otherInvocations(id)
		}
	}
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless(), output: question}
	res, err := drive.Story(context.Background(), id, opts(perf))
	if err != nil || !res.Parked {
		t.Fatalf("want parked, got %v %+v", err, res)
	}
	parks, _ := e.driveRows(id)
	if len(parks) != 1 {
		t.Fatalf("want one park comment, got %d", len(parks))
	}
	wantStopPayload(t, parks[0], question)
}

// AC3: the same for a named performer dispatched inside story-set.
func TestDriveLastMessageNamedPerformerCleanQuestion(t *testing.T) {
	e := newEnv(t, routeDone, namedRoute)
	d := newStubDispatcher()
	d.out["in_progress"] = question
	verb.SetExecutorDispatcher(d)
	e.gater.always["done"] = true
	id := e.story()
	e.gater.onGate = func(to string) {
		if to == "done" {
			e.otherInvocations(id)
		}
	}
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerDispatched}, agents: map[string]string{"in_progress": "coder"}}
	res, err := drive.Story(context.Background(), id, opts(perf))
	if err != nil || !res.Parked {
		t.Fatalf("want parked, got %v %+v", err, res)
	}
	parks, _ := e.driveRows(id)
	if len(parks) != 1 {
		t.Fatalf("want one park comment, got %d", len(parks))
	}
	p := wantStopPayload(t, parks[0], question)
	if p["performer"] != "coder" || p["step"] != "in_progress" {
		t.Errorf("payload = %v, want the coder on in_progress", p)
	}
}

// AC3: a rework relay that parks quotes the relay's own close row, not the older
// dispatch's output.
func TestDriveLastMessageReworkRelay(t *testing.T) {
	e := newEnv(t, routeDone, reworkRoute)
	d := newStubDispatcher()
	d.out["in_progress"] = "old"
	verb.SetExecutorDispatcher(d)
	e.gater.always["done"] = true
	id := e.story()
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerDispatched}, agents: map[string]string{"in_progress": "coder"}}
	o := opts(perf)
	o.Rework = func(ctx context.Context, rid string) (drive.Rework, error) {
		if err := verb.AppendAgentInvocation(ctx, rid, map[string]any{"agent": "coder", "phase": "close", "last_message": "relay says Z"}); err != nil {
			t.Fatal(err)
		}
		e.otherInvocations(rid)
		return drive.Rework{Outcome: "budget", Rounds: 2, LastObjection: "STILL-NOT-READY"}, nil
	}
	res, err := drive.Story(context.Background(), id, o)
	if err != nil || !res.Parked {
		t.Fatalf("want parked, got %v %+v", err, res)
	}
	parks, _ := e.driveRows(id)
	if len(parks) != 1 {
		t.Fatalf("want one park comment, got %d", len(parks))
	}
	p := wantStopPayload(t, parks[0], "relay says Z")
	if p["performer"] != "coder" {
		t.Errorf("performer = %v, want the relay's coder", p["performer"])
	}
}

// AC4: a context that ends mid-step still leaves a record, even though the park
// is written after it ended.
func TestDriveRecordsStopOnCancel(t *testing.T) {
	for _, tc := range []struct {
		name, done string
		wantPark   bool
	}{{"parks", routeDone, true}, {"no park state", routeNoPark, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.done, workRoute)
			id := e.story()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			perf := &fakePerformers{
				kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless(), output: "working on it",
				onPerform: func(workitem.Item, string, string) error { cancel(); return nil },
			}
			res, err := drive.Story(ctx, id, opts(perf))
			parks, stops := e.driveRows(id)
			if tc.wantPark {
				if err != nil || !res.Parked || len(parks) != 1 || len(stops) != 0 {
					t.Fatalf("want a park comment, got %v %+v (%d parks, %d stops)", err, res, len(parks), len(stops))
				}
				p := wantStopPayload(t, parks[0], "working on it")
				if !strings.Contains(p["reason"].(string), "interrupted") {
					t.Errorf("reason = %v, want the interruption named", p["reason"])
				}
				return
			}
			var stopped *drive.Stopped
			if !errors.As(err, &stopped) || len(parks) != 0 || len(stops) != 1 {
				t.Fatalf("want a drive-stopped row, got %v (%d parks, %d stops)", err, len(parks), len(stops))
			}
			wantStopPayload(t, stops[0], "working on it")
		})
	}
}

// AC5: where the workflow has no park state, the stop is a drive-stopped row, once.
func TestDriveStoppedNoParkState(t *testing.T) {
	e := newEnv(t, routeNoPark, workRoute)
	e.gater.always["done"] = true
	id := e.story()
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless(), output: question}
	_, err := drive.Story(context.Background(), id, opts(perf))
	var stopped *drive.Stopped
	if !errors.As(err, &stopped) || stopped.ID != id || stopped.Status != "in_progress" {
		t.Fatalf("want a *Stopped at in_progress, got %v", err)
	}
	if want := id + " stopped at in_progress: " + stopped.Reason; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	parks, stops := e.driveRows(id)
	if len(parks) != 0 || len(stops) != 1 || e.status(id) != "in_progress" {
		t.Fatalf("want one drive-stopped row and nothing parked, got %d parks, %d stops, status %q", len(parks), len(stops), e.status(id))
	}
	p := wantStopPayload(t, stops[0], question)
	if !strings.Contains(p["park_error"].(string), "no park state") || stops[0].Body != stopped.Reason {
		t.Errorf("row = %v body %q, want the park error and the reason", p, stops[0].Body)
	}
}

// AC5: where the park itself is refused, the stop is a drive-stopped row, once.
func TestDriveStoppedParkRefused(t *testing.T) {
	e := newEnv(t, routeDone, workRoute)
	e.gater.always["done"] = true
	e.gater.always["blocked"] = true
	id := e.story()
	perf := &fakePerformers{kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless(), output: question}
	_, err := drive.Story(context.Background(), id, opts(perf))
	var stopped *drive.Stopped
	if !errors.As(err, &stopped) || stopped.ParkErr == nil {
		t.Fatalf("want a *Stopped carrying why the park failed, got %v", err)
	}
	_, stops := e.driveRows(id)
	if len(stops) != 1 || e.status(id) != "in_progress" {
		t.Fatalf("want one drive-stopped row and the story where it was, got %d stops, status %q", len(stops), e.status(id))
	}
	p := wantStopPayload(t, stops[0], question)
	if !strings.Contains(p["park_error"].(string), "park at blocked") {
		t.Errorf("park_error = %v, want the refused park named", p["park_error"])
	}
}

// AC9: every way a drive stops names the step, the performer and what was missing.
func TestDriveStopReasonsNameStepAndPerformer(t *testing.T) {
	executorPerf := func(onPerform func(workitem.Item, string, string) error) *fakePerformers {
		return &fakePerformers{
			kinds: map[string]verb.PerformerKind{"in_progress": verb.PerformerExecutor}, executor: headless(),
			output: question, onPerform: onPerform,
		}
	}
	coderPerf := func(step string) *fakePerformers {
		return &fakePerformers{kinds: map[string]verb.PerformerKind{step: verb.PerformerDispatched}, agents: map[string]string{step: "coder"}}
	}
	cases := []struct {
		name, done, route, want string
		setup                   func(e *env, d *stubDispatcher)
		perf                    func() *fakePerformers
		rework                  drive.Reworker
	}{
		{name: "reject budget spent", done: routeDone, route: budgetedRoute, want: "step in_progress, performer executor:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["done"] = true }, perf: func() *fakePerformers { return executorPerf(nil) }},
		{name: "unbudgeted rejections", done: routeDone, route: workRoute, want: "step backlog, performer gate:stub-gate:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["in_progress"] = true }, perf: func() *fakePerformers { return &fakePerformers{} }},
		{name: "executor rejects", done: routeDone, route: workRoute, want: "step in_progress, performer executor:",
			perf: func() *fakePerformers {
				return executorPerf(func(workitem.Item, string, string) error { return &verb.PerformerReject{Notes: "merge conflict"} })
			}},
		{name: "executor fails", done: routeDone, route: workRoute, want: "step in_progress, performer executor:",
			perf: func() *fakePerformers {
				return executorPerf(func(workitem.Item, string, string) error { return errors.New("rate limited") })
			}},
		{name: "rework relay", done: routeDone, route: reworkRoute, want: "step in_progress, performer coder:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["done"] = true }, perf: func() *fakePerformers { return coderPerf("in_progress") },
			rework: func(context.Context, string) (drive.Rework, error) {
				return drive.Rework{Outcome: "budget", Rounds: 2, LastObjection: "STILL"}, nil
			}},
		{name: "rework relay fails", done: routeDone, route: reworkRoute, want: "step in_progress, performer coder:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["done"] = true }, perf: func() *fakePerformers { return coderPerf("in_progress") },
			rework: func(context.Context, string) (drive.Rework, error) { return drive.Rework{}, errors.New("no session") }},
		{name: "set parks it", done: routeDone, route: namedRoute, want: "step in_progress, performer coder:",
			setup: func(_ *env, d *stubDispatcher) {
				d.rejects["in_progress"] = &verb.PerformerReject{Notes: "premise is wrong"}
			},
			perf: func() *fakePerformers { return coderPerf("in_progress") }},
		{name: "refused after the first move", done: routeDone, route: workRoute, want: "step done, performer none:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.errs["done"] = errors.New("gate down") },
			perf:  func() *fakePerformers { return executorPerf(nil) }},
		{name: "named performer fails", done: routeDone, route: lateNamedRoute, want: "step done, performer coder:",
			setup: func(_ *env, d *stubDispatcher) { d.errs["done"] = errors.New("coder crashed") },
			perf:  func() *fakePerformers { return coderPerf("done") }},
		{name: "no park state", done: routeNoPark, route: workRoute, want: "step in_progress, performer executor:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["done"] = true }, perf: func() *fakePerformers { return executorPerf(nil) }},
		{name: "park refused", done: routeDone, route: workRoute, want: "step in_progress, performer executor:",
			setup: func(e *env, _ *stubDispatcher) { e.gater.always["done"] = true; e.gater.always["blocked"] = true },
			perf:  func() *fakePerformers { return executorPerf(nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.done, tc.route)
			d := newStubDispatcher()
			if strings.Contains(tc.route, `agent = "coder"`) {
				d.out["in_progress"], d.out["done"] = "coder output", "coder output"
				verb.SetExecutorDispatcher(d)
			}
			if tc.setup != nil {
				tc.setup(e, d)
			}
			id := e.story()
			o := opts(tc.perf())
			o.Rework = tc.rework
			res, err := drive.Story(context.Background(), id, o)
			var reason string
			var stopped *drive.Stopped
			switch {
			case errors.As(err, &stopped):
				reason = stopped.Reason
			case err != nil:
				t.Fatalf("drive: %v", err)
			case res.Parked:
				reason = res.Reason
			default:
				t.Fatalf("the drive neither parked nor stopped: %+v", res)
			}
			t.Logf("reason: %s", reason)
			if !strings.HasPrefix(reason, tc.want) {
				t.Errorf("reason = %q, want it to begin %q", reason, tc.want)
			}
			parks, stops := e.driveRows(id)
			if len(parks)+len(stops) == 0 {
				t.Fatal("no park comment or drive-stopped row was written")
			}
			for _, row := range append(parks, stops...) {
				wantStopPayload(t, row, "*")
			}
		})
	}
}
