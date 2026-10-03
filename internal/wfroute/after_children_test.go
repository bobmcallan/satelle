package wfroute

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

const afterChildrenDoneBody = `[epic-parent]
obligations = ["raised", "ready", "merged", "children-resolved"]
`

const afterChildrenStepBody = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[merged]
status = "merging"
agent = "executor"
after_children = "coded"
requires = ["ready"]

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["merged"]
`

func afterChildrenRoute(t *testing.T, stepBody string) Route {
	t.Helper()
	lists, err := wfdot.ParseDone(afterChildrenDoneBody)
	if err != nil {
		t.Fatalf("ParseDone: %v", err)
	}
	cat, err := wfdot.ParseSteps(stepBody)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	list, err := wfdot.ListFor(lists, "epic-parent")
	if err != nil {
		t.Fatalf("ListFor: %v", err)
	}
	spec, err := wfdot.BuildRoute(list, cat, nil)
	if err != nil {
		t.Fatalf("BuildRoute: %v", err)
	}
	return Build(spec, "wf", nil, nil, nil)
}

func TestAfterChildrenOnTheRouteJSONAndRendered(t *testing.T) {
	r := afterChildrenRoute(t, afterChildrenStepBody)
	if got := stepAt(t, r, "merging").AfterChildren; got != "coded" {
		t.Fatalf("merging step AfterChildren = %q, want coded", got)
	}
	if got := stepAt(t, r, "ready").AfterChildren; got != "" {
		t.Errorf("an undeclared step carries AfterChildren %q", got)
	}
	raw, err := json.Marshal(stepAt(t, r, "merging"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"after_children":"coded"`) {
		t.Errorf("route JSON must carry the key: %s", raw)
	}
	if out := r.Render("ready"); !strings.Contains(out, `every child has discharged "coded"`) {
		t.Errorf("rendered route must say what entry waits on:\n%s", out)
	}
}

func TestAfterChildrenAbsentRendersNothing(t *testing.T) {
	without := strings.Replace(afterChildrenStepBody, "after_children = \"coded\"\n", "", 1)
	r := afterChildrenRoute(t, without)
	raw, _ := json.Marshal(stepAt(t, r, "merging"))
	if strings.Contains(string(raw), "after_children") {
		t.Errorf("an absent key must stay off the JSON: %s", raw)
	}
	if out := r.Render("ready"); strings.Contains(out, "discharged") {
		t.Errorf("an absent key must render nothing:\n%s", out)
	}
}
