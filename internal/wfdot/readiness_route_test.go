package wfdot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repo route with ONE readiness step (sty_5262592e), read from the same files
// a repo activates as .satelle/workflows/{done,step}.toml — so what these tests
// derive is what runs.

func readinessRoute(t *testing.T, category string, tags []string) (Spec, List, Catalogue) {
	t.Helper()
	d, err := os.ReadFile(filepath.Join("testdata", "readiness_done.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := os.ReadFile(filepath.Join("testdata", "readiness_step.toml"))
	if err != nil {
		t.Fatal(err)
	}
	lists, err := ParseDone(string(d))
	if err != nil {
		t.Fatalf("ParseDone: %v", err)
	}
	cat, err := ParseSteps(string(s))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	l, err := ListFor(lists, category)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := BuildRoute(l, cat, tags)
	if err != nil {
		t.Fatalf("BuildRoute(%s): %v", category, err)
	}
	return spec, l, cat
}

// AC1: a story's route has ONE readiness step between backlog and in_progress —
// no `ready` state — that proposes its plan, is budgeted, and is gated by the four
// readiness reviewers; a rejected edge has no destination but backlog.
func TestRepoRouteHasOneReadinessStep(t *testing.T) {
	spec, _, _ := readinessRoute(t, "feature", nil)
	if problems := Validate(spec); len(problems) != 0 {
		t.Fatalf("derived spec does not validate: %v", problems)
	}
	if _, ok := spec.StateNamed("ready"); ok {
		t.Error("a story's route must carry no ready state")
	}
	if !spec.HasEdge("backlog", "plan") || !spec.HasEdge("plan", "in_progress") || spec.HasEdge("backlog", "in_progress") {
		t.Errorf("route must run backlog→plan→in_progress: %+v", spec.Transitions)
	}
	plan, _ := spec.StateNamed("plan")
	if !plan.Propose || plan.RejectBudget != 3 || plan.Agent != "planner" || plan.Skill != "plan" {
		t.Errorf("readiness step = %+v, want a planner that proposes with a budget of 3", plan)
	}
	var into, out Transition
	for _, tr := range spec.Transitions {
		switch {
		case tr.From == "backlog" && tr.To == "plan":
			into = tr
		case tr.From == "plan" && tr.To == "in_progress":
			out = tr
		}
	}
	want := []string{
		"satelle-story-intent-review", "satelle-story-plan-review",
		"satelle-story-architecture-review", "satelle-story-integration-coverage-review",
	}
	if strings.Join(into.Skills, ",") != strings.Join(want, ",") || into.Parallel != 4 {
		t.Errorf("backlog→plan gates = %v parallel %d, want the four readiness reviewers in parallel", into.Skills, into.Parallel)
	}
	if strings.Join(out.Skills, ",") != "satelle-definition-unchanged-check" {
		t.Errorf("plan→in_progress gates = %v, want only the definition-unchanged check", out.Skills)
	}
	if !spec.HasEdge("plan", "backlog") {
		t.Error("the declared recover entry must give plan→backlog")
	}
	if !spec.HasEdge("integration", "in_progress") || !spec.HasEdge("release", "in_progress") {
		t.Error("the original recover edges must survive the array form")
	}
	if got := spec.Start(); got != "backlog" {
		t.Errorf("Start() = %q, want backlog", got)
	}
}

// AC2 (route half): the definition is editable in backlog and plan and frozen from
// in_progress on, resolved from the freeze step by route order.
func TestRepoRouteFreezesAtImplementation(t *testing.T) {
	spec, _, _ := readinessRoute(t, "feature", nil)
	for status, want := range map[string]bool{
		"backlog": true, "plan": true,
		"in_progress": false, "integration": false, "release": false, "done": false, "blocked": false,
	} {
		if got, ok := spec.DefinitionEditable(status); !ok || got != want {
			t.Errorf("DefinitionEditable(%q) = %v/%v, want %v", status, got, ok, want)
		}
	}
}

// Containers keep their own ready step: an epic-parent still runs
// backlog→ready→done, is not budgeted or proposed, and freezes when it leaves
// backlog exactly as before.
func TestRepoRouteContainersKeepTheirReadyStep(t *testing.T) {
	for _, category := range []string{"epic-parent", "parent"} {
		spec, _, _ := readinessRoute(t, category, nil)
		ready, ok := spec.StateNamed("ready")
		if !ok || ready.Agent != "ready-reviewer" || ready.Propose || ready.RejectBudget != 0 {
			t.Errorf("%s: ready = %+v (found %v), want the ready-reviewer step with no readiness knobs", category, ready, ok)
		}
		if _, ok := spec.StateNamed("plan"); ok {
			t.Errorf("%s: a container route has no plan step", category)
		}
		if !spec.HasEdge("backlog", "ready") || !spec.HasEdge("ready", "done") {
			t.Errorf("%s: route must run backlog→ready→done: %+v", category, spec.Transitions)
		}
		if got, _ := spec.DefinitionEditable("ready"); got {
			t.Errorf("%s: the legacy rule applies — frozen once it leaves backlog", category)
		}
		if got, _ := spec.DefinitionEditable("backlog"); !got {
			t.Errorf("%s: editable in backlog", category)
		}
	}
}

// Every other lane is untouched by the readiness step: no knobs, no freeze step.
func TestRepoRouteOtherLanesCarryNoReadinessKnobs(t *testing.T) {
	for _, category := range []string{"docs", "substrate", "task"} {
		spec, _, _ := readinessRoute(t, category, nil)
		for _, st := range spec.States {
			if st.Propose || st.Freeze || st.RejectBudget != 0 {
				t.Errorf("%s: state %q carries a readiness knob: %+v", category, st.Name, st)
			}
		}
	}
}
