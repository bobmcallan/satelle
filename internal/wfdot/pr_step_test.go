package wfdot

import "testing"

// The pull-request step is authored configuration: pr_step = true on a step sets
// IsPRState for its status, and a route with no such key has none.
func TestPRStepKeyMarksTheState(t *testing.T) {
	body := reworkStepBody("pr_step = true\n")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if !stepProviding(t, cat, "coded").PRStep {
		t.Error("step did not carry pr_step = true")
	}
	spec, err := ParseRoute(reworkDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if !spec.IsPRState("in_progress") || !spec.HasPRState() {
		t.Error("IsPRState(in_progress) / HasPRState = false, want the marked step")
	}
	for _, other := range []string{"backlog", "no-such-status"} {
		if spec.IsPRState(other) {
			t.Errorf("IsPRState(%q) = true, want false", other)
		}
	}

	spec, err = ParseRoute(reworkDone, reworkStepBody(""), "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if spec.IsPRState("in_progress") || spec.HasPRState() {
		t.Error("a route without pr_step must mark no state")
	}
}
