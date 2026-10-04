package wfroute

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

func TestRoutePrintsPanelAndCombine(t *testing.T) {
	spec, err := wfdot.ParseRoute(`["*"]
obligations = ["raised", "coded", "closed"]
`, `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
reviewers = ["panel-review"]
panel = ["seat-a", "seat-b"]
combine = "satelle-panel-all-accept"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`, "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	route := Build(spec, "default", nil, nil, nil)
	out := route.Render("backlog")
	for _, want := range []string{"panel seat-a, seat-b", "combine satelle-panel-all-accept"} {
		if !strings.Contains(out, want) {
			t.Errorf("route missing %q:\n%s", want, out)
		}
	}
}
