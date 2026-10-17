package wfdot

import "testing"

// `untrusted_input = true` parses onto the Step and through the derived route onto
// the State; a step without it carries false.
func TestUntrustedInputKnob(t *testing.T) {
	body := reworkStepBody("untrusted_input = true\n")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if !stepProviding(t, cat, "coded").UntrustedInput {
		t.Error("step did not carry untrusted_input = true")
	}
	spec, err := ParseRoute(reworkDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if state, ok := spec.StateNamed("in_progress"); !ok || !state.UntrustedInput {
		t.Errorf("route state = %+v (found %v), want the knob carried through", state, ok)
	}

	absent, err := ParseSteps(reworkStepBody(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if stepProviding(t, absent, "coded").UntrustedInput {
		t.Error("a step without the knob must not be untrusted")
	}
	spec, err = ParseRoute(reworkDone, reworkStepBody(""), "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if state, _ := spec.StateNamed("in_progress"); state.UntrustedInput {
		t.Error("a route state without the knob must not be untrusted")
	}
}
