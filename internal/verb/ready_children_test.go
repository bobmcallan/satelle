package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_2b737ed5: a container readiness step declares whether `story ready` also
// readies each child. With ready_children = false the epic's own round is the
// whole readiness and each child is planned when the wave lets it start.

// withReadyChildren declares ready_children = value on the fixture's container
// readiness step.
func withReadyChildren(stepToml, value string) string {
	return strings.Replace(stepToml, "waits_on_children = true\n", "waits_on_children = true\nready_children = "+value+"\n", 1)
}

func newEpicWithReadyChildren(t *testing.T, value string, kids int) (*readyRig, workitem.Item, []workitem.Item) {
	t.Helper()
	r := newRigFor(t, withReadyChildren(readyStepToml("ready", readyReviewers), value))
	epic := newStoryOf(t, "epic-parent", "epic", "epic:rr")
	var children []workitem.Item
	for i := 0; i < kids; i++ {
		children = append(children, newStoryOf(t, "feature", "child"+string(rune('a'+i)), "epic:rr"))
	}
	r.g.verd = func(it workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{} // the epic's ready step has no reviewers
		}
		return acceptAll()
	}
	return r, epic, children
}

// AC1: ready_children = false runs only the epic's own round; no child planner or
// child reviewer is dispatched and the epic is marked on the accepted round.
func TestReadyEpicWithReadyChildrenFalsePlansNoChild(t *testing.T) {
	r, epic, kids := newEpicWithReadyChildren(t, "false", 2)
	res := readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyMarked || res.NoMark {
		t.Fatalf("epic: %+v", res)
	}
	if !hasMark(t, epic.ID) {
		t.Error("the epic must be marked on its own accepted round")
	}
	if r.d.calls[epic.ID] != 1 {
		t.Errorf("the epic's own step runs once: %d", r.d.calls[epic.ID])
	}
	if len(res.Children) != 0 {
		t.Errorf("no child result is reported: %+v", res.Children)
	}
	for _, k := range kids {
		if r.d.calls[k.ID] != 0 || r.g.calls[k.ID] != 0 {
			t.Errorf("child %s: planner dispatches %d, reviewer rounds %d, want none", k.ID, r.d.calls[k.ID], r.g.calls[k.ID])
		}
		if hasMark(t, k.ID) {
			t.Errorf("child %s must not be marked by the epic's readiness", k.ID)
		}
	}
}

// AC2: an explicit ready_children = true behaves like the absent key — each child
// is readied.
func TestReadyEpicWithReadyChildrenTrueReadiesEachChild(t *testing.T) {
	r, epic, kids := newEpicWithReadyChildren(t, "true", 2)
	res := readyCall(t, epic.ID)[0]
	if res.Outcome != verb.ReadyMarked || len(res.Children) != 2 {
		t.Fatalf("epic: %+v", res)
	}
	for _, k := range kids {
		if !hasMark(t, k.ID) || r.d.calls[k.ID] != 1 {
			t.Errorf("child %s must be readied once and marked", k.ID)
		}
	}
}

// AC5: with ready_children = false a child of the marked epic is engaged with no
// plan and runs its own readiness step (planner and plan review) at engagement.
func TestChildOfReadyChildrenFalseEpicIsPlannedAtEngagement(t *testing.T) {
	r, epic, kids := newEpicWithReadyChildren(t, "false", 1)
	if res := readyCall(t, epic.ID)[0]; res.Outcome != verb.ReadyMarked {
		t.Fatalf("epic: %+v", res)
	}
	var w struct{ Runnable []string }
	if err := json.Unmarshal(call(t, "story-wave", map[string]any{"id": epic.ID}), &w); err != nil || !carries(w.Runnable, kids[0].ID) {
		t.Fatalf("the wave must list the child: %v %+v", err, w)
	}
	if r.d.calls[kids[0].ID] != 0 {
		t.Fatalf("no child plan may exist before engagement: %d", r.d.calls[kids[0].ID])
	}
	if err := setStatus(t, kids[0].ID, "plan"); err != nil {
		t.Fatalf("a wave-listed child of the marked epic must engage: %v", err)
	}
	if r.d.calls[kids[0].ID] != 1 || r.g.calls[kids[0].ID] == 0 {
		t.Errorf("the child's own readiness step must run at engagement: planner %d, reviewer rounds %d", r.d.calls[kids[0].ID], r.g.calls[kids[0].ID])
	}
	if got := statusOf(t, kids[0].ID).Status; got != "plan" {
		t.Errorf("child status = %q, want plan", got)
	}
}

// AC7: a ready round that chains the children with depends-on tags leaves the
// wave exactly one runnable child, with no `sequential` key on the step.
func TestReadyRoundChainingDependsOnLeavesOneRunnableChild(t *testing.T) {
	r := newRigFor(t, withReadyChildren(changesStepMark("ready"), "false"))
	verb.SetWorkflowResolver(stubWorkflow{})
	epic := newStoryOf(t, "epic-parent", "epic", "epic:rr")
	a := newStoryOf(t, "feature", "childa", "epic:rr")
	b := newStoryOf(t, "feature", "childb", "epic:rr")
	c := newStoryOf(t, "feature", "childc", "epic:rr")
	r.g.verd = func(it workitem.Item, to string) verb.GateDecision {
		if to == "ready" {
			return verb.GateDecision{}
		}
		return acceptAll()
	}
	waveOf := func() []string {
		var w struct{ Runnable []string }
		if err := json.Unmarshal(call(t, "story-wave", map[string]any{"id": epic.ID}), &w); err != nil {
			t.Fatal(err)
		}
		return w.Runnable
	}
	if got := waveOf(); len(got) != 3 {
		t.Fatalf("before the round the three independent children are runnable: %v", got)
	}
	r.d.perform = performerReports(epic.ID, []agentartifact.Change{
		{Op: "add-tags", Story: b.ID, Tags: []string{"depends-on:" + a.ID}},
		{Op: "add-tags", Story: c.ID, Tags: []string{"depends-on:" + b.ID}},
	}, "")
	if res := readyCall(t, epic.ID)[0]; res.Outcome != verb.ReadyMarked {
		t.Fatalf("epic: %+v", res)
	}
	if got := waveOf(); len(got) != 1 || got[0] != a.ID {
		t.Errorf("wave runnable = %v, want exactly [%s]", got, a.ID)
	}
}
