package wfroute

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_5262592e: the route renders a step's declared propose / reject_budget /
// freeze knobs, so the orchestrator reads the readiness contract off
// `satelle story route` instead of a skill's prose.

const knobStepBody = `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
propose = true
reject_budget = 3
reviewers = ["gate-a"]
requires = ["raised"]

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

const knobDoneBody = "[feature]\nobligations = [\"raised\", \"readied\", \"coded\", \"closed\"]\n"

func TestRouteRendersReadinessKnobs(t *testing.T) {
	spec, err := wfdot.ParseRoute(knobDoneBody, knobStepBody, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	route := Build(spec, "default", nil, nil, nil)
	byStatus := map[string]Step{}
	for _, s := range route.Steps {
		byStatus[s.Status] = s
	}
	if p := byStatus["plan"]; !p.Propose || p.RejectBudget != 3 || p.Freeze {
		t.Errorf("plan step = %+v, want propose and a budget of 3", p)
	}
	if c := byStatus["in_progress"]; !c.Freeze || c.Propose {
		t.Errorf("in_progress step = %+v, want freeze", c)
	}
	out := route.Render("backlog")
	for _, want := range []string{"performer runs before the entry gates", "rejected 3 time(s) before the story parks", "definition freezes on entry"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered route must say %q:\n%s", want, out)
		}
	}
}

// A step declaring none of the knobs renders exactly as it did before them.
func TestRouteWithoutKnobsRendersNoKnobLine(t *testing.T) {
	steps := strings.NewReplacer("propose = true\n", "", "reject_budget = 3\n", "", "freeze = true\n", "").Replace(knobStepBody)
	spec, err := wfdot.ParseRoute(knobDoneBody, steps, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := Build(spec, "default", nil, nil, nil).Render("backlog")
	if strings.Contains(out, "freezes") || strings.Contains(out, "rejected") || strings.Contains(out, "before the entry gates") {
		t.Errorf("no knob line expected:\n%s", out)
	}
}
