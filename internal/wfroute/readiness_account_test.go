package wfroute

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_87403c0e: the route gives the one account of what a readiness round does —
// it changes no status, a container's round readies its children only when its
// step leaves ready_children on, and satelle (not the driver) applies the criteria.

const waitsOnChildrenLine = "waits_on_children = true\n"

func TestRouteStatesWhatContainerReadinessDoesToChildren(t *testing.T) {
	const (
		on  = "readying the container also readies each backlog child; each child stays in backlog"
		off = "readying the container plans no child; each child is planned when the wave lets it start"
	)
	for _, tc := range []struct {
		name, line string
		want, not  string
	}{
		{"absent", "", on, off},
		{"true", "ready_children = true\n", on, off},
		{"false", "ready_children = false\n", off, on},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := strings.Replace(afterChildrenStepBody, waitsOnChildrenLine, waitsOnChildrenLine+tc.line, 1)
			out := afterChildrenRoute(t, steps).Render("ready")
			if !strings.Contains(out, tc.want) {
				t.Errorf("route must say %q:\n%s", tc.want, out)
			}
			if strings.Contains(out, tc.not) {
				t.Errorf("route must not say %q:\n%s", tc.not, out)
			}
		})
	}
}

func TestRouteStatesChildPlanningOnlyOnAContainer(t *testing.T) {
	spec, err := wfdot.ParseRoute(knobDoneBody, knobStepBody, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := Build(spec, "default", nil, nil, nil).Render("backlog")
	if strings.Contains(out, "each backlog child") || strings.Contains(out, "plans no child") {
		t.Errorf("a step that does not wait on children says nothing about child planning:\n%s", out)
	}
}

func TestRouteStatesAMarkedRoundChangesNoStatus(t *testing.T) {
	spec, err := wfdot.ParseRoute(knobDoneBody, knobStepBody, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := Build(spec, "default", nil, nil, nil).Render("backlog")
	want := `marks the story with the tag "ready" and changes no status; the edge is taken by ` + "`satelle story set`"
	if !strings.Contains(out, want) {
		t.Errorf("route must say %q:\n%s", want, out)
	}
}

func TestRouteStatesSatelleAppliesTheCriteria(t *testing.T) {
	steps := strings.Replace(knobStepBody, "freeze = true\n", "freeze = true\napply_criteria = { doc = \"plan\", heading = \"Acceptance criteria\" }\n", 1)
	spec, err := wfdot.ParseRoute(knobDoneBody, steps, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := Build(spec, "default", nil, nil, nil).Render("backlog")
	want := `entry applies the criteria authored under "Acceptance criteria" in the plan document; the driver does not copy them by hand`
	if !strings.Contains(out, want) {
		t.Errorf("route must say %q:\n%s", want, out)
	}
}
