package wfgovern

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfroute"
)

// An advisor must attach only to a step the route actually SELECTED
// (sty_a7316b06). The catalogue is shared and stage names repeat across route
// families by design, so a catalogue walk hands one family's advisor to every
// route with a step of that name.
//
// Fixtures use invented names: these assert a structural rule that must hold for
// any repo, so baking this repo's categories or skills in would make the test
// about satelle rather than about the rule.
func advisorFixture() RouteSource {
	done := `[alpha]
obligations = ["raised", "built", "closed"]
park = { state = "parked", gate = "park-gate", advisor = "park-helper", advisor_skill = "park-rubric" }
cancel = { state = "cancelled", gate = "cancel-gate" }

[beta]
obligations = ["raised", "built", "children-resolved"]
cancel = { state = "cancelled", gate = "cancel-gate" }
`
	step := `[raised]
status = "backlog"
start = true

[built]
status = "build"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["built"]
advise = { agent = "retro", skill = "retro-rubric" }

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["built"]
`
	return RouteSource{Done: done, Step: step}
}

func advisorFor(t *testing.T, category, step string) *wfroute.Advisor {
	t.Helper()
	d, err := RouteSpecFor(advisorFixture(), category, nil)
	if err != nil {
		t.Fatalf("RouteSpecFor(%q): %v", category, err)
	}
	route := wfroute.Build(d.Spec, DerivedRouteName, nil, d.Advisors)
	for _, st := range route.Steps {
		if st.Status == step {
			return st.Advisor
		}
	}
	t.Fatalf("category %q has no step %q", category, step)
	return nil
}

// Two categories whose routes share the stage name `done`: alpha's discharges
// `closed` and declares an advisor, beta's discharges `children-resolved` and
// declares none. Before the fix both reported alpha's advisor.
func TestAdvisorsFromOnlySelectedSteps(t *testing.T) {
	if a := advisorFor(t, "alpha", "done"); a == nil {
		t.Error("alpha's done step declares an advisor and must keep it")
	} else if a.Agent != "retro" || a.Skill != "retro-rubric" {
		t.Errorf("alpha advisor = %+v, want retro @retro-rubric", a)
	}

	if a := advisorFor(t, "beta", "done"); a != nil {
		t.Errorf("beta's done step declares NO advisor, but the route claims %+v — "+
			"an advisor leaked across route families sharing a stage name", a)
	}
}

// The park advisor comes off the List, which is already per-category, so it is
// not exposed to the same hazard — and the narrowing must not drop it.
func TestParkAdvisorSurvivesSelection(t *testing.T) {
	d, err := RouteSpecFor(advisorFixture(), "alpha", nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range d.Advisors {
		if a.Step == "parked" && a.Agent == "park-helper" && a.Skill == "park-rubric" {
			found = true
		}
	}
	if !found {
		t.Errorf("the park advisor must survive: %+v", d.Advisors)
	}

	// beta declares no park at all, so it must carry no park advisor either.
	db, err := RouteSpecFor(advisorFixture(), "beta", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range db.Advisors {
		if a.Agent == "park-helper" {
			t.Errorf("beta declares no park, but carries the park advisor: %+v", a)
		}
	}
}
