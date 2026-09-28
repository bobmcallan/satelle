package wfdot

import "testing"

// sty_7f3e6fd3: a step may declare that a container idles there while its
// children are driven. The declaration is the route's; Go names no status.

const waitsSteps = `[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["raised"]
`

const waitsDone = `["*"]
obligations = ["raised", "ready", "children-resolved"]
`

func TestWaitsOnChildrenParsesOntoStepAndSpec(t *testing.T) {
	cat, err := ParseSteps(waitsSteps)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if !stepProviding(t, cat, "ready").WaitsOnChildren {
		t.Error("ready must carry waits_on_children")
	}
	if stepProviding(t, cat, "raised").WaitsOnChildren {
		t.Error("an undeclared step must not wait on children")
	}

	spec, err := ParseRoute(waitsDone, waitsSteps, "epic-parent", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if !spec.WaitsOnChildren("ready") {
		t.Error("Spec.WaitsOnChildren(ready) = false, want true")
	}
	for _, other := range []string{"backlog", "done", "no-such-status"} {
		if spec.WaitsOnChildren(other) {
			t.Errorf("Spec.WaitsOnChildren(%q) = true, want false", other)
		}
	}
}
