package wfroute

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// The route-display half of sty_8e0b29a0 AC1.

const reworkDoneBody = `[feature]
obligations = ["raised", "coded", "closed"]
`

const reworkStepBody = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
skills = ["code"]
requires = ["raised"]
rework = { consult = "reviewer", rounds = 3 }

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// reworkRoute builds a small route whose coded step declares a rework loop,
// through the SAME chain a story's route takes: parse → select → derive
// annotations → Build.
func reworkRoute(t *testing.T, stepBody string) Route {
	t.Helper()
	lists, err := wfdot.ParseDone(reworkDoneBody)
	if err != nil {
		t.Fatalf("ParseDone: %v", err)
	}
	cat, err := wfdot.ParseSteps(stepBody)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	list, err := wfdot.ListFor(lists, "feature")
	if err != nil {
		t.Fatalf("ListFor: %v", err)
	}
	selected, err := wfdot.SelectSteps(list, cat, nil)
	if err != nil {
		t.Fatalf("SelectSteps: %v", err)
	}
	spec, err := wfdot.BuildRoute(list, cat, nil)
	if err != nil {
		t.Fatalf("BuildRoute: %v", err)
	}
	return Build(spec, "wf", nil, AdvisorsFrom(list, selected), ReworksFrom(selected))
}

func TestReworkOnTheRouteAndRendered(t *testing.T) {
	r := reworkRoute(t, reworkStepBody)
	st := stepAt(t, r, "in_progress")
	if st.Rework == nil {
		t.Fatalf("in_progress step carries no rework: %+v", st)
	}
	if st.Rework.Consult != "reviewer" || st.Rework.Rounds != 3 {
		t.Errorf("rework = %+v; want consult=reviewer rounds=3", st.Rework)
	}
	// Only the step that declares it.
	if done := stepAt(t, r, "done"); done.Rework != nil {
		t.Errorf("done step carries a rework it never declared: %+v", done.Rework)
	}

	out := r.Render("in_progress")
	for _, want := range []string{
		"rework: consult reviewer, up to 3 round(s)",
		"satelle story rework",
		"the outcome is context, the entry gate still decides",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered route is missing %q:\n%s", want, out)
		}
	}
}

// TestReworkAbsentRendersNothing is the display half of AC1's no-op guarantee:
// the same route without the key renders byte-identically to a route built by a
// binary that had never heard of rework.
func TestReworkAbsentRendersNothing(t *testing.T) {
	without := strings.ReplaceAll(reworkStepBody, "rework = { consult = \"reviewer\", rounds = 3 }\n", "")
	r := reworkRoute(t, without)
	if st := stepAt(t, r, "in_progress"); st.Rework != nil {
		t.Fatalf("rework synthesised where none was authored: %+v", st.Rework)
	}
	if len(ReworksFrom(nil)) != 0 {
		t.Errorf("ReworksFrom(nil) must be empty")
	}
	out := r.Render("in_progress")
	if strings.Contains(out, "rework") {
		t.Errorf("route without a rework key mentions rework:\n%s", out)
	}
}
