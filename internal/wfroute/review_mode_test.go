package wfroute

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_93359e0f: the route shows which gates review fail-fast, for an edge-named
// reviewer a step lists and for a [[gate]] node; a gate with no mode shows none.
func TestRouteShowsFailFastGates(t *testing.T) {
	spec, err := wfdot.ParseRoute(`["*"]
obligations = ["raised", "coded", "closed"]
`, `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
reviewers = ["gate-a", "gate-b"]
fail_fast = ["gate-b"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[[gate]]
skill = "scoped-judge"
on = ["in_progress"]
review_mode = "fail-fast"
`, "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	route := Build(spec, "default", nil, nil, nil)
	modes := map[string]string{}
	for _, st := range route.Steps {
		for _, rv := range st.Reviewers {
			modes[rv.Skill] = rv.ReviewMode
		}
	}
	for skill, want := range map[string]string{"gate-a": "", "gate-b": "fail-fast", "scoped-judge": "fail-fast"} {
		if got, ok := modes[skill]; !ok || got != want {
			t.Errorf("%s: ReviewMode = %q (listed %v), want %q", skill, got, ok, want)
		}
	}
	out := route.Render("backlog")
	if !strings.Contains(out, "gate-b (fail-fast review)") || strings.Contains(out, "gate-a (fail-fast review)") {
		t.Errorf("render must mark only the fail-fast gate:\n%s", out)
	}
	b, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"review_mode":"fail-fast"`); n != 2 {
		t.Errorf("route JSON names fail-fast %d times, want 2 (gate-b and scoped-judge): %s", n, b)
	}
}
