package wfdot

import (
	"strings"
	"testing"
)

// sty_af0b1761: `apply_changes = { ops, tags }` parses onto the Step and through
// the derived route onto the State; a half-declared one, one on a step that does
// not declare propose, and an unknown op are refused when the route is loaded.

const applyChangesLine = `apply_changes = { ops = ["add-tags", "create-child"], tags = ["order:", "lane:"] }` + "\n"

func TestApplyChangesKnob(t *testing.T) {
	body := reworkStepBody("propose = true\n" + applyChangesLine)
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	if strings.Join(st.ApplyChangeOps, ",") != "add-tags,create-child" || strings.Join(st.ApplyChangeTags, ",") != "order:,lane:" {
		t.Errorf("step = %v / %v, want the declared ops and tag prefixes", st.ApplyChangeOps, st.ApplyChangeTags)
	}
	spec, err := ParseRoute(reworkDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	state, ok := spec.StateNamed("in_progress")
	if !ok || strings.Join(state.ApplyChangeOps, ",") != "add-tags,create-child" || strings.Join(state.ApplyChangeTags, ",") != "order:,lane:" {
		t.Errorf("route state = %+v (found %v), want the knob carried through", state, ok)
	}

	absent, err := ParseSteps(reworkStepBody("propose = true\n"))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	if st := stepProviding(t, absent, "coded"); len(st.ApplyChangeOps) != 0 || len(st.ApplyChangeTags) != 0 {
		t.Errorf("step without the knob carries %v / %v, want neither", st.ApplyChangeOps, st.ApplyChangeTags)
	}
}

func TestApplyChangesHalfDeclaredIsRefused(t *testing.T) {
	for name, line := range map[string]string{
		"no tags":       `apply_changes = { ops = ["add-tags"] }`,
		"no ops":        `apply_changes = { tags = ["order:"] }`,
		"empty ops":     `apply_changes = { ops = [], tags = ["order:"] }`,
		"blank tags":    `apply_changes = { ops = ["add-tags"], tags = [" "] }`,
		"empty declare": `apply_changes = {}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSteps(reworkStepBody("propose = true\n" + line + "\n"))
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, want := range []string{"step.toml", `step "coded"`, "apply_changes", "half a declaration"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestApplyChangesRequiresPropose(t *testing.T) {
	_, err := ParseSteps(reworkStepBody(applyChangesLine))
	if err == nil {
		t.Fatal("apply_changes on a step that does not declare propose must be refused")
	}
	for _, want := range []string{"step.toml", `step "coded"`, "apply_changes requires propose = true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestApplyChangesUnknownOpIsRefused(t *testing.T) {
	_, err := ParseSteps(reworkStepBody("propose = true\n" + `apply_changes = { ops = ["add-tags", "delete-story"], tags = ["order:"] }` + "\n"))
	if err == nil || !strings.Contains(err.Error(), `"delete-story"`) || !strings.Contains(err.Error(), "add-tags, create-child") {
		t.Fatalf("want the unknown op named with the allowed set, got %v", err)
	}
}
