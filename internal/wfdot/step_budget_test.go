package wfdot

import (
	"strings"
	"testing"
)

// sty_a7914904: a step may declare its performer's context and turn budgets.
// They ride the step onto its State, absent means unset, and a negative value is
// refused at parse. The numbers are fixture data — the binary ships none.

func TestStepBudgetsParseOntoStepAndState(t *testing.T) {
	steps := strings.Replace(readinessSteps,
		"freeze = true\n",
		"freeze = true\ncontext_budget = 4000\nturn_budget = 9\n", 1)
	cat, err := ParseSteps(steps)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	if st.ContextBudget != 4000 || st.TurnBudget != 9 {
		t.Errorf("coded budgets = %d/%d, want 4000/9", st.ContextBudget, st.TurnBudget)
	}
	spec, err := ParseRoute(readinessDone, steps, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	coded, ok := spec.StateNamed("in_progress")
	if !ok || coded.ContextBudget != 4000 || coded.TurnBudget != 9 {
		t.Errorf("in_progress state = %+v, want the step's budgets carried", coded)
	}
	plan, _ := spec.StateNamed("plan")
	if plan.ContextBudget != 0 || plan.TurnBudget != 0 {
		t.Errorf("a step that declares no budget carries none, got %d/%d", plan.ContextBudget, plan.TurnBudget)
	}
}

func TestStepNegativeBudgetRefused(t *testing.T) {
	for _, key := range []string{"context_budget", "turn_budget"} {
		steps := strings.Replace(readinessSteps, "freeze = true\n", "freeze = true\n"+key+" = -1\n", 1)
		if _, err := ParseSteps(steps); err == nil || !strings.Contains(err.Error(), "negative") {
			t.Errorf("%s = -1: err = %v, want a negative refusal", key, err)
		}
	}
}
