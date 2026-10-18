package wfdot

import (
	"strings"
	"testing"
)

// sty_59720b8b: reviewer_carry is a closed enum on a step. It reaches the edge
// that enters the step; any other value is refused naming the step.
func TestReviewerCarryParsesOntoTheEntryEdge(t *testing.T) {
	carry := strings.Replace(readinessSteps, "reject_budget = 3", "reject_budget = 3\nreviewer_carry = \"until_accept\"", 1)
	spec, err := ParseRoute(readinessDone, carry, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	edgeCarry := func(spec Spec, from, to string) string {
		for _, tr := range spec.Transitions {
			if tr.From == from && tr.To == to {
				return tr.ReviewerCarry
			}
		}
		t.Fatalf("no %s→%s edge", from, to)
		return ""
	}
	if got := edgeCarry(spec, "backlog", "plan"); got != CarryUntilAccept {
		t.Errorf("backlog→plan ReviewerCarry = %q, want %q", got, CarryUntilAccept)
	}
	if got := edgeCarry(spec, "plan", "in_progress"); got != "" {
		t.Errorf("plan→in_progress ReviewerCarry = %q, want it unset (the key is per step)", got)
	}
	if got := edgeCarry(readinessSpec(t), "backlog", "plan"); got != "" {
		t.Errorf("an absent key parses to %q, want empty", got)
	}
}

func TestReviewerCarryUnknownValueIsRefusedByStep(t *testing.T) {
	bad := strings.Replace(readinessSteps, "reject_budget = 3", "reject_budget = 3\nreviewer_carry = \"bogus\"", 1)
	_, err := ParseSteps(bad)
	if err == nil {
		t.Fatal("want an error for an unknown reviewer_carry value")
	}
	for _, want := range []string{"step.toml", `step "readied"`, "reviewer_carry", `"bogus"`, CarryUntilAccept} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}
