package wfdot

import (
	"strings"
	"testing"
)

// sty_4d9df9a0: `apply_criteria = { doc, heading }` parses onto the Step and
// through the derived route onto the State, a half-authored one is refused by
// name, and a step without it carries neither field.

func TestApplyCriteriaKnob(t *testing.T) {
	body := reworkStepBody(`apply_criteria = { doc = "plan", heading = "Acceptance criteria" }` + "\n")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	if st.ApplyCriteriaDoc != "plan" || st.ApplyCriteriaHeading != "Acceptance criteria" {
		t.Errorf("step = %q / %q, want plan / Acceptance criteria", st.ApplyCriteriaDoc, st.ApplyCriteriaHeading)
	}
	spec, err := ParseRoute(reworkDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	state, ok := spec.StateNamed("in_progress")
	if !ok || state.ApplyCriteriaDoc != "plan" || state.ApplyCriteriaHeading != "Acceptance criteria" {
		t.Errorf("route state = %+v (found %v), want the knob carried through", state, ok)
	}

	absent, err := ParseSteps(reworkStepBody(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if st := stepProviding(t, absent, "coded"); st.ApplyCriteriaDoc != "" || st.ApplyCriteriaHeading != "" {
		t.Errorf("step without the knob carries %q / %q, want neither", st.ApplyCriteriaDoc, st.ApplyCriteriaHeading)
	}
}

func TestApplyCriteriaHalfDeclaredIsRefused(t *testing.T) {
	for name, line := range map[string]string{
		"empty doc":     `apply_criteria = { doc = "", heading = "Acceptance criteria" }`,
		"empty heading": `apply_criteria = { doc = "plan", heading = "" }`,
		"no heading":    `apply_criteria = { doc = "plan" }`,
		"blank doc":     `apply_criteria = { doc = "  ", heading = "Acceptance criteria" }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSteps(reworkStepBody(line + "\n"))
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, want := range []string{"step.toml", `step "coded"`, "apply_criteria"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}
