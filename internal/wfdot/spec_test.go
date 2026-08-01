package wfdot

import (
	"reflect"
	"strings"
	"testing"
)

// The Spec METHODS used to be exercised through DOT fixtures, because DOT text
// was how a Spec came into existence. With the DOT front end retired
// (sty_d953c5d8) the only constructor is BuildRoute, so these cases are stated
// as Spec literals: the same shapes the retired fixtures built, asserted against
// the same methods. route_test.go covers the construction side; this file covers
// what the constructed thing answers.

// projectShaped is the lifecycle shape this repo runs: a gated spine with a
// planner, an executor, an always-on scoped gate, a park state and a cancel
// sink. It is the fixture most of the retired DOT cases used.
func projectShaped() Spec {
	return Spec{
		States: []State{
			{Name: "backlog", Shape: "Mdiamond"},
			{Name: "plan", Agent: "planner", Skill: "plan"},
			{Name: "in_progress", Agent: "executor", Skill: "code"},
			{Name: "done", Shape: "Msquare"},
			{Name: "cancelled", Agent: "reviewer", Skill: "cancel-review"},
			{Name: "blocked", Agent: "reviewer", Skill: "blocked-review", From: []string{"*"}},
			{Name: "gate_estimate", Skill: "estimate-check", On: []string{"in_progress", "done"}},
			{Name: "gate_design", Agent: "reviewer", Skill: "design-review",
				On: []string{"done"}, AppliesTo: []string{"surface:ui"}},
			{Name: "gate_summary", Agent: "reviewer", Skill: StepSummarySkill, Mandatory: true},
		},
		Transitions: []Transition{
			{From: "backlog", To: "plan", Skill: "intent-review", Skills: []string{"intent-review"}},
			{From: "plan", To: "in_progress", Skill: "plan-review", Skills: []string{"plan-review"}},
			{From: "in_progress", To: "done", Skill: "done-review",
				Skills: []string{"done-review", "scope-review"}, Agent: "reviewer", Parallel: 4},
			{From: "backlog", To: "cancelled", Skill: "cancel-review", Skills: []string{"cancel-review"}},
			{From: "plan", To: "cancelled", Skill: "cancel-review", Skills: []string{"cancel-review"}},
			{From: "in_progress", To: "cancelled", Skill: "cancel-review", Skills: []string{"cancel-review"}},
			{From: "plan", To: "blocked", Skill: "blocked-review", Skills: []string{"blocked-review"}},
			{From: "in_progress", To: "blocked", Skill: "blocked-review", Skills: []string{"blocked-review"}},
			{From: "blocked", To: "cancelled", Skill: "cancel-review", Skills: []string{"cancel-review"}},
		},
	}
}

func TestValidateAcceptsAProjectShapedSpec(t *testing.T) {
	if problems := Validate(projectShaped()); len(problems) != 0 {
		t.Fatalf("a well-formed spec must validate, got %v", problems)
	}
}

func TestValidateNamesTheDefects(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"no states", Spec{}, "no states"},
		{
			"edge from an unknown state",
			Spec{States: []State{{Name: "backlog"}}, Transitions: []Transition{{From: "nope", To: "backlog"}}},
			`from unknown state "nope"`,
		},
		{
			"edge to an unknown state",
			Spec{States: []State{{Name: "backlog"}}, Transitions: []Transition{{From: "backlog", To: "nope"}}},
			`to unknown state "nope"`,
		},
		{
			"done is not terminal",
			Spec{
				States:      []State{{Name: "backlog"}, {Name: "done"}},
				Transitions: []Transition{{From: "backlog", To: "done"}, {From: "done", To: "backlog"}},
			},
			`"done" must be terminal`,
		},
		{
			"applies_to on a performing spine node",
			Spec{
				States: []State{{Name: "backlog"}, {Name: "in_progress", Agent: "executor",
					Skill: "code", AppliesTo: []string{"surface:ui"}}, {Name: "done"}},
				Transitions: []Transition{{From: "backlog", To: "in_progress"}, {From: "in_progress", To: "done"}},
			},
			"is not supported without on=",
		},
		{
			"a performing on= node that is also on an edge",
			Spec{
				States: []State{{Name: "backlog"}, {Name: "aug", Agent: "executor",
					Skill: "x", On: []string{"in_progress"}}, {Name: "done"}},
				Transitions: []Transition{{From: "backlog", To: "aug"}, {From: "aug", To: "done"}},
			},
			"must be edge-less",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(Validate(tc.spec), " | ")
			if !strings.Contains(got, tc.want) {
				t.Errorf("Validate = %q, want a problem mentioning %q", got, tc.want)
			}
		})
	}
}

func TestStartIsTheStateWithNoInboundEdge(t *testing.T) {
	if got := projectShaped().Start(); got != "backlog" {
		t.Errorf("Start = %q, want backlog", got)
	}
	// Every state reachable from another: no determinable start.
	cyclic := Spec{
		States:      []State{{Name: "a"}, {Name: "b"}},
		Transitions: []Transition{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	if got := cyclic.Start(); got != "" {
		t.Errorf("Start = %q, want empty when every state has an inbound edge", got)
	}
}

func TestStepSummaryDeclarationAndBinding(t *testing.T) {
	spec := projectShaped()
	declared, mandatory := spec.StepSummary()
	if !declared || !mandatory {
		t.Errorf("StepSummary = (%v, %v), want (true, true)", declared, mandatory)
	}
	agent, declared, mandatory := spec.StepSummaryBinding()
	if !declared || !mandatory || agent != "reviewer" {
		t.Errorf("StepSummaryBinding = (%q, %v, %v), want (reviewer, true, true)", agent, declared, mandatory)
	}
	// A spec that declares none says so — the opt-in is the declaration.
	bare := Spec{States: []State{{Name: "backlog"}, {Name: "done"}}}
	if declared, _ := bare.StepSummary(); declared {
		t.Error("a spec with no step-summary node must not report one declared")
	}
}

func TestScopedReviewersSplitByTags(t *testing.T) {
	spec := projectShaped()
	// The estimate gate is unscoped: it fires on its states regardless of tags.
	enq, skipped := spec.ScopedReviewersSplit("in_progress", nil)
	if len(enq) != 1 || enq[0].Skill != "estimate-check" {
		t.Fatalf("in_progress enqueued = %+v, want the unscoped estimate gate", enq)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing is scoped away on in_progress, got %+v", skipped)
	}
	// The design gate is surface-scoped: skipped without the tag, enqueued with it.
	enq, skipped = spec.ScopedReviewersSplit("done", nil)
	if len(enq) != 1 || enq[0].Skill != "estimate-check" {
		t.Errorf("done enqueued without tags = %+v, want the estimate gate only", enq)
	}
	if len(skipped) != 1 || skipped[0].Skill != "design-review" {
		t.Errorf("done skipped without tags = %+v, want the surface-scoped design gate", skipped)
	}
	enq, _ = spec.ScopedReviewersSplit("done", []string{"surface:ui"})
	if len(enq) != 2 {
		t.Errorf("done enqueued with surface:ui = %+v, want both gates", enq)
	}
	// ScopedReviewers is the enqueued half of the same answer.
	if got := spec.ScopedReviewers("done", []string{"surface:ui"}); len(got) != len(enq) {
		t.Errorf("ScopedReviewers = %d, want the enqueued half (%d)", len(got), len(enq))
	}
}

func TestTagsMatchAppliesTo(t *testing.T) {
	cases := []struct {
		appliesTo, tags []string
		want            bool
	}{
		{nil, []string{"surface:ui"}, true},                      // unscoped matches anything
		{[]string{"surface:ui"}, []string{"surface:ui"}, true},   // exact
		{[]string{"surface:ui"}, []string{"surface:cli"}, false}, // different value
		{[]string{"surface:ui"}, nil, false},                     // scoped, untagged story
		{[]string{"a", "b"}, []string{"b"}, true},                // any-match
	}
	for _, tc := range cases {
		if got := tagsMatchAppliesTo(tc.appliesTo, tc.tags); got != tc.want {
			t.Errorf("tagsMatchAppliesTo(%v, %v) = %v, want %v", tc.appliesTo, tc.tags, got, tc.want)
		}
	}
}

func TestPerformingAndEditCapableStates(t *testing.T) {
	spec := projectShaped()
	if got := spec.PerformingStates(); !reflect.DeepEqual(got, []string{"plan", "in_progress"}) {
		t.Errorf("PerformingStates = %v, want [plan in_progress]", got)
	}
	// Edit-capable is the in-loop subset: a NAMED agent (planner) is dispatched
	// and isolated, so it is performing without being edit-capable.
	if got := spec.EditCapableStates(); !reflect.DeepEqual(got, []string{"in_progress"}) {
		t.Errorf("EditCapableStates = %v, want [in_progress]", got)
	}
	if !spec.IsEditCapableState("in_progress") {
		t.Error("in_progress must be edit-capable")
	}
	for _, name := range []string{"plan", "backlog", "done", "blocked", "cancelled"} {
		if spec.IsEditCapableState(name) {
			t.Errorf("%s must not be edit-capable", name)
		}
	}
	if !spec.IsPerformingState("plan") || spec.IsPerformingState("done") {
		t.Error("IsPerformingState must follow PerformingStates")
	}
}

func TestStateAgentAndEngagingStates(t *testing.T) {
	spec := projectShaped()
	if agent, ok := spec.StateAgent("in_progress"); !ok || agent != "executor" {
		t.Errorf("StateAgent(in_progress) = (%q, %v), want (executor, true)", agent, ok)
	}
	if _, ok := spec.StateAgent("nope"); ok {
		t.Error("StateAgent of an unknown state must report not-found")
	}
	// A park state and a cancel sink are reviewer states: entering them engages
	// nothing, so the edit and commit gates stay shut there.
	got := spec.NonTerminalEngagingStates()
	if !reflect.DeepEqual(got, []string{"plan", "in_progress"}) {
		t.Errorf("NonTerminalEngagingStates = %v, want [plan in_progress]", got)
	}
}

func TestTerminalAndParkStates(t *testing.T) {
	spec := projectShaped()
	if !spec.IsTerminalState("done") {
		t.Error("done is the terminal success state")
	}
	if !spec.IsParkState("blocked") || !spec.IsParkState("cancelled") {
		t.Error("a reviewer state that is not the start reads as parked/off-route")
	}
	if spec.IsParkState("backlog") {
		t.Error("the start state must never read as parked")
	}
	if spec.IsTerminalState("in_progress") {
		t.Error("a performing state is not terminal")
	}
}

func TestAdvanceOptionsOffersForwardTargetsOnly(t *testing.T) {
	spec := projectShaped()
	opts := spec.AdvanceOptions("plan")
	if len(opts) == 0 {
		t.Fatal("plan must offer at least one onward transition")
	}
	if opts[0].To != "in_progress" {
		t.Errorf("first advance from plan = %q, want in_progress", opts[0].To)
	}
	if len(opts[0].Gates) == 0 {
		t.Error("the offered advance must carry the gates that admit it")
	}
	for _, o := range opts {
		if o.To == "cancelled" || o.To == "blocked" {
			t.Errorf("AdvanceOptions offered the off-route exit %q", o.To)
		}
	}
	if got := len(spec.AdvanceOptions("done")); got != 0 {
		t.Errorf("a terminal state offers no advance, got %d", got)
	}
}

func TestExecutorPathAndPerStateSkills(t *testing.T) {
	spec := projectShaped()
	// Sorted, not path-ordered — the set is what the actionability check needs.
	got := spec.ExecutorPathToDoneSkills()
	if !reflect.DeepEqual(got, []string{"code", "plan"}) {
		t.Errorf("ExecutorPathToDoneSkills = %v, want [code plan]", got)
	}
	if tagged := spec.ExecutorPathToDoneSkillsFor([]string{"surface:ui"}); !reflect.DeepEqual(tagged, got) {
		t.Errorf("tags must not remove a spine rubric: %v vs %v", tagged, got)
	}
	if s := spec.ExecutorSkillsFor("in_progress", nil); !reflect.DeepEqual(s, []string{"code"}) {
		t.Errorf("ExecutorSkillsFor(in_progress) = %v, want [code]", s)
	}
	if s := spec.ExecutorSkillsFor("done", nil); len(s) != 0 {
		t.Errorf("a non-performing state carries no executor rubric, got %v", s)
	}
}

func TestExecutorAugmentationIsTagScoped(t *testing.T) {
	spec := projectShaped()
	spec.States = append(spec.States, State{
		Name: "aug_ui", Agent: "executor", Skill: "ui-polish",
		On: []string{"in_progress"}, AppliesTo: []string{"surface:ui"},
	})
	if problems := Validate(spec); len(problems) != 0 {
		t.Fatalf("an edge-less executor augmentation is legal, got %v", problems)
	}
	for _, st := range spec.States {
		if st.Name == "aug_ui" && !st.IsAugmentation() {
			t.Error("an edge-less performing on= node is an augmentation")
		}
	}
	if got := spec.ExecutorSkillsFor("in_progress", nil); !reflect.DeepEqual(got, []string{"code"}) {
		t.Errorf("an untagged story gets the spine rubric only, got %v", got)
	}
	got := spec.ExecutorSkillsFor("in_progress", []string{"surface:ui"})
	if !reflect.DeepEqual(got, []string{"code", "ui-polish"}) {
		t.Errorf("a matching tag appends the augmentation, got %v", got)
	}
}

func TestEdgeQueries(t *testing.T) {
	spec := projectShaped()
	if !spec.HasEdge("plan", "in_progress") || !spec.Declares("plan", "in_progress") {
		t.Error("a declared edge must be reported")
	}
	if spec.HasEdge("backlog", "done") {
		t.Error("an undeclared edge must not be reported — that is how a skipped step is caught")
	}
	got := spec.Successors("plan")
	want := map[string]bool{"in_progress": true, "cancelled": true, "blocked": true}
	if len(got) != len(want) {
		t.Fatalf("Successors(plan) = %v, want %d targets", got, len(want))
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("Successors(plan) returned unexpected %q", s)
		}
	}
}

func TestSummariserAndPerformingClassification(t *testing.T) {
	summary := State{Name: "gate_summary", Agent: "reviewer", Skill: StepSummarySkill, Mandatory: true}
	if !summary.IsSummariser() {
		t.Error("the step-summary node is a summariser")
	}
	if summary.IsPerforming() {
		t.Error("a summariser never performs")
	}
	named := State{Name: "plan", Agent: "planner", Skill: "plan"}
	if !named.IsPerforming() {
		t.Error("a NAMED agent is a performer — it does the work, isolated")
	}
	reviewer := State{Name: "cancelled", Agent: "reviewer", Skill: "cancel-review"}
	if reviewer.IsPerforming() {
		t.Error("a reviewer judges, never performs")
	}
}
