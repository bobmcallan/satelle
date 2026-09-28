package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_5262592e: one readiness step. The fixture route declares the three step
// knobs — propose, freeze, reject_budget — and a plan→backlog recover edge; the
// behaviour under test is the binary reading them, not this repo's step names.

const readinessDoneToml = `["*"]
obligations = ["raised", "readied", "coded", "closed"]
park = { state = "blocked", gate = "park-gate" }
recover = { step = "backlog", from = ["plan"] }
`

func readinessStepToml(readied string) string {
	return `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["gate-a", "gate-b", "gate-c", "gate-d"]
requires = ["raised"]
` + readied + `

[coded]
status = "in_progress"
agent = "executor"
freeze = true
requires = ["readied"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

var (
	readinessWF        = routeHalves(readinessDoneToml, readinessStepToml("propose = true\nreject_budget = 3\n"))
	readinessNoPropose = routeHalves(readinessDoneToml, readinessStepToml("reject_budget = 3\n"))
)

// scriptedGater answers each Gate call from a script and records the order.
type scriptedGater struct {
	mu    *sync.Mutex
	log   *[]string
	verd  func(to string, n int) verb.GateDecision
	calls map[string]int
}

func (g *scriptedGater) Gate(_ context.Context, _ workitem.Item, to string) (verb.GateDecision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls[to]++
	*g.log = append(*g.log, "gate:"+to)
	return g.verd(to, g.calls[to]), nil
}

// fourReviewers is one presentation: three reject, one accepts.
func fourReviewers(notes string) verb.GateDecision {
	rv := func(skill string, ok bool) verb.ReviewerVerdict {
		n := ""
		if !ok {
			n = notes + " (" + skill + ")"
		}
		return verb.ReviewerVerdict{Skill: skill, Accept: ok, Notes: n}
	}
	return verb.GateDecision{Gated: true, Accept: false, Skill: "gate-a", Reviewers: []verb.ReviewerVerdict{
		rv("gate-a", false), rv("gate-b", false), rv("gate-c", false), rv("gate-d", true),
	}}
}

func acceptAll() verb.GateDecision {
	return verb.GateDecision{Gated: true, Accept: true, Skill: "gate-a", Reviewers: []verb.ReviewerVerdict{
		{Skill: "gate-a", Accept: true}, {Skill: "gate-b", Accept: true},
	}}
}

type readinessRig struct {
	log    []string
	mu     sync.Mutex
	gater  *scriptedGater
	planed int
}

// newReadinessRig wires the route, a scripted gater and a recording dispatcher.
// The dispatcher attaches a `plan` doc the way a real planner's artifact lands.
func newReadinessRig(t *testing.T, wf map[string]string, verd func(to string, n int) verb.GateDecision) *readinessRig {
	t.Helper()
	wireWithWorkflows(t, wf)
	verb.SetStoryDir(filepath.Join(t.TempDir(), ".satelle", "stories"))
	r := &readinessRig{}
	r.gater = &scriptedGater{mu: &r.mu, log: &r.log, verd: verd, calls: map[string]int{}}
	verb.SetTransitionGater(r.gater)
	verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "plan" {
			return verb.DispatchResult{}, nil
		}
		r.mu.Lock()
		r.log = append(r.log, "perform:"+to+"@"+it.Status)
		r.planed++
		r.mu.Unlock()
		call(t, "story-doc-attach", map[string]any{"story_id": it.ID, "name": "plan", "type": "plan", "body": "the plan"})
		return verb.DispatchResult{Dispatched: true, Agent: "planner", Skill: "plan"}, nil
	}))
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		verb.SetExecutorDispatcher(nil)
	})
	return r
}

func newFeature(t *testing.T) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "B", "acceptance_criteria": "1. one", "category": "feature",
	}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func ledgerRows(t *testing.T, id, kind string) []ledger.Entry {
	t.Helper()
	var all, out []ledger.Entry
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id}), &all); err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func attemptOf(t *testing.T, e ledger.Entry) string {
	t.Helper()
	var p struct {
		Attempt string `json:"attempt"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Attempt
}

// AC1: a rejected readiness edge leaves the story in backlog, and the rows of one
// presentation share one attempt id.
func TestReadinessRejectLeavesStoryInBacklog(t *testing.T) {
	r := newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return fourReviewers("AC1 contradicts AC2") })
	it := newFeature(t)

	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want a reject, got %v", err)
	}
	if got := statusOf(t, it.ID); got.Status != "backlog" {
		t.Fatalf("status = %q, want backlog", got.Status)
	}
	rejects := ledgerRows(t, it.ID, ledger.KindReviewReject)
	accepts := ledgerRows(t, it.ID, ledger.KindReviewAccept)
	if len(rejects) != 3 || len(accepts) != 1 {
		t.Fatalf("rows = %d rejects, %d accepts, want 3 and 1", len(rejects), len(accepts))
	}
	id := attemptOf(t, rejects[0])
	if id == "" {
		t.Fatal("verdict rows must carry an attempt id")
	}
	for _, e := range append(rejects, accepts...) {
		if attemptOf(t, e) != id {
			t.Errorf("one presentation must share one attempt id, got %q vs %q", attemptOf(t, e), id)
		}
	}
	if got := r.gater.calls["plan"]; got != 1 {
		t.Errorf("gate calls = %d, want 1", got)
	}
}

// AC2 + AC3: editable in backlog after a rejection (each edit recorded with
// actor, old and new value), and frozen once the story enters in_progress.
func TestDefinitionEditableUntilInProgressAndRecorded(t *testing.T) {
	round := 0
	newReadinessRig(t, readinessWF, func(to string, _ int) verb.GateDecision {
		if to == "plan" {
			round++
			if round == 1 {
				return fourReviewers("AC1 promises what the binary should not keep")
			}
		}
		return acceptAll()
	})
	it := newFeature(t)
	dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})

	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. narrowed"})
	call(t, "story-set", map[string]any{"id": it.ID, "title": "T2", "body": "B2"})
	call(t, "story-set", map[string]any{"id": it.ID, "priority": "high", "tags": []string{"x"}})
	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. narrowed"}) // identical resubmit

	edits := ledgerRows(t, it.ID, ledger.KindDefinitionEdited)
	if len(edits) != 3 {
		t.Fatalf("definition_edited rows = %d, want 3 (AC once, title, body; a tag/priority edit and a resubmit write none)", len(edits))
	}
	var ac struct{ Field, Old, New, Actor string }
	if err := json.Unmarshal(edits[0].Payload, &ac); err != nil {
		t.Fatal(err)
	}
	if ac.Field != "acceptance_criteria" || ac.Old != "1. one" || ac.New != "1. narrowed" || ac.Actor == "" || edits[0].Actor == "" {
		t.Errorf("edit row = %+v actor %q, want field, old, new and an actor", ac, edits[0].Actor)
	}

	// Re-present: accepted, story in plan (readiness done), still editable.
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if got := statusOf(t, it.ID).Status; got != "plan" {
		t.Fatalf("status = %q, want plan", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. narrowed again"})

	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. sneaky"})
	if !strings.Contains(err.Error(), "acceptance_criteria") || !strings.Contains(err.Error(), `entry to "in_progress"`) {
		t.Fatalf("want the frozen error naming the freeze step, got %v", err)
	}
	for _, field := range []string{"title", "body"} {
		dispatchErr(t, "story-set", map[string]any{"id": it.ID, field: "changed"})
	}
	if got := len(ledgerRows(t, it.ID, ledger.KindDefinitionEdited)); got != 4 {
		t.Errorf("a refused edit writes no row: %d rows, want 4", got)
	}
}

// AC4: the performer runs before the gate, the artifact it attached is there when
// the gate runs, and a performer error means no gate and no transition.
func TestProposeRunsPerformerBeforeGate(t *testing.T) {
	var planAtGate bool
	var sid string
	r := newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	inner := r.gater.verd
	r.gater.verd = func(to string, n int) verb.GateDecision {
		if to == "plan" {
			var docs []struct{ Name string }
			json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": sid}), &docs)
			for _, d := range docs {
				planAtGate = planAtGate || d.Name == "plan"
			}
		}
		return inner(to, n)
	}
	it := newFeature(t)
	sid = it.ID
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})

	if got := strings.Join(r.log, ","); got != "perform:plan@backlog,gate:plan" {
		t.Errorf("call order = %s, want the performer on backlog, then the gate", got)
	}
	if !planAtGate {
		t.Error("the plan artifact must exist when the gate runs")
	}
	if got := statusOf(t, it.ID).Status; got != "plan" {
		t.Errorf("status = %q, want plan", got)
	}
}

func TestProposePerformerErrorSkipsGateAndKeepsBacklog(t *testing.T) {
	r := newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	verb.SetExecutorDispatcher(dispatcherFunc(func(context.Context, workitem.Item, string) (verb.DispatchResult, error) {
		return verb.DispatchResult{}, errors.New("planner crashed")
	}))
	it := newFeature(t)
	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "planner crashed") {
		t.Fatalf("want the dispatch refusal, got %v", err)
	}
	if r.gater.calls["plan"] != 0 {
		t.Errorf("the gate must not run after a performer error, ran %d", r.gater.calls["plan"])
	}
	if got := statusOf(t, it.ID).Status; got != "backlog" {
		t.Errorf("status = %q, want backlog", got)
	}
}

// Without `propose` the order is unchanged: gate, then performer.
func TestWithoutProposeGateRunsBeforePerformer(t *testing.T) {
	r := newReadinessRig(t, readinessNoPropose, func(string, int) verb.GateDecision { return acceptAll() })
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if got := strings.Join(r.log, ","); got != "gate:plan,perform:plan@backlog" {
		t.Errorf("call order = %s, want gate then performer", got)
	}
}

// A performer that judges the premise wrong parks from backlog (before any gate)
// and a later resume returns the story to backlog.
func TestProposePerformerRejectParksFromBacklogAndResumes(t *testing.T) {
	r := newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, _ workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "plan" {
			return verb.DispatchResult{}, nil
		}
		return verb.DispatchResult{Agent: "planner"}, &verb.PerformerReject{Notes: "premise wrong: AC2 names a flag that does not exist"}
	}))
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	after := statusOf(t, it.ID)
	if after.Status != "blocked" || after.ParkOrigin != "backlog" {
		t.Fatalf("status %q origin %q, want blocked parked from backlog", after.Status, after.ParkOrigin)
	}
	if r.gater.calls["plan"] != 0 {
		t.Errorf("no readiness gate may run for a premise reject, ran %d", r.gater.calls["plan"])
	}
	found := false
	for _, e := range ledgerRows(t, it.ID, ledger.KindComment) {
		found = found || strings.Contains(e.Body, "AC2 names a flag")
	}
	if !found {
		t.Error("the performer's notes must be on the timeline")
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "backlog"})
	if got := statusOf(t, it.ID).Status; got != "backlog" {
		t.Errorf("resume: status = %q, want backlog", got)
	}
}

// AC5: the unit is a rejected PRESENTATION. Three rounds (each with three
// rejecting reviewers) spend a budget of 3; the fourth is refused with the last
// objection quoted, parking with that reason is accepted, and a resume resets.
func TestRejectBudgetCountsRoundsParksAndResets(t *testing.T) {
	r := newReadinessRig(t, readinessWF, func(to string, n int) verb.GateDecision {
		if to == "plan" {
			return fourReviewers("objection-" + string(rune('0'+n)))
		}
		return acceptAll()
	})
	it := newFeature(t)
	for i := 0; i < 3; i++ {
		dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	}
	if got := len(ledgerRows(t, it.ID, ledger.KindReviewReject)); got != 9 {
		t.Fatalf("reject rows = %d, want 9 (3 rounds x 3 reviewers)", got)
	}
	performed := r.planed

	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	for _, want := range []string{"edge rejected 3 times", "last objection", "objection-3", "decision needed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q, got: %v", want, err)
		}
	}
	if r.planed != performed || r.gater.calls["plan"] != 3 {
		t.Errorf("a spent budget must refuse before any performer or gate: performed %d→%d, gate calls %d", performed, r.planed, r.gater.calls["plan"])
	}

	call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"})
	if got := statusOf(t, it.ID).Status; got != "blocked" {
		t.Fatalf("park after a spent budget: status = %q, want blocked", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "backlog"})

	// Reset: the next presentation reaches the gate again (4th call overall).
	dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if r.gater.calls["plan"] != 4 {
		t.Errorf("after resume the budget restarts: gate calls = %d, want 4", r.gater.calls["plan"])
	}
}

// AC4/AC2 window: an edit after readiness was accepted is enumerated against the
// accepted edge, and the recover edge plan→backlog followed by a re-presented,
// accepted readiness clears it.
func TestDefinitionEditsSinceAcceptedEdgeAndRecover(t *testing.T) {
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})

	count := func() int {
		var res verb.DefinitionEditsResult
		if err := json.Unmarshal(call(t, "story-definition-edits", map[string]any{"id": it.ID, "since_edge": "backlog:plan"}), &res); err != nil {
			t.Fatal(err)
		}
		return res.Count
	}
	if got := count(); got != 0 {
		t.Fatalf("no edit since the accepted edge yet, got %d", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. edited in plan"})
	if got := count(); got != 1 {
		t.Fatalf("an edit in plan postdates the accepted edge, got %d", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "backlog"}) // the declared recover edge
	if got := statusOf(t, it.ID).Status; got != "backlog" {
		t.Fatalf("recover: status = %q, want backlog", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if got := count(); got != 0 {
		t.Fatalf("a re-accepted readiness edge supersedes the edit, got %d", got)
	}
	if err := dispatchErr(t, "story-definition-edits", map[string]any{"id": it.ID, "since_edge": "nonsense"}); !strings.Contains(err.Error(), "<from>:<to>") {
		t.Errorf("a malformed edge must be named, got %v", err)
	}
}
