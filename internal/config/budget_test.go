package config

import (
	"strings"
	"testing"
)

// TestBudgetKeysDecode: context_budget / turn_budget are read from a binding and
// from [defaults], and an absent key is the zero "unset" — Go ships no number.
func TestBudgetKeysDecode(t *testing.T) {
	ac, err := loadAgentsBody(`
[defaults]
context_budget = 900
turn_budget    = 30

[coder]
role = "agent"
command = "claude -p --max-turns {max_turns}"
context_budget = 500
turn_budget    = 12

[reviewer]
command = "claude -p"
`)
	if err != nil {
		t.Fatalf("loadAgentsBody: %v", err)
	}
	if got := ac.Defaults.Budget(); got != (Budget{Context: 900, Turns: 30}) {
		t.Errorf("defaults budget = %+v", got)
	}
	coder, ok := ac.NamedBinding("coder")
	if !ok {
		t.Fatal("coder binding missing")
	}
	if got := coder.Budget(); got != (Budget{Context: 500, Turns: 12}) {
		t.Errorf("coder budget = %+v", got)
	}
	if got := ac.Reviewer.Budget(); got.Set() {
		t.Errorf("a binding that sets no budget must carry none, got %+v", got)
	}
}

// TestResolveBudgetOrder: step, then binding, then [defaults]; each bound is
// resolved on its own, so a step that sets only turns still inherits context.
func TestResolveBudgetOrder(t *testing.T) {
	ac := AgentsConfig{Defaults: AgentsDefaults{ContextBudget: 900, TurnBudget: 30}}
	b := AgentBinding{ContextBudget: 500}
	cases := []struct {
		name string
		step Budget
		want Budget
	}{
		{"binding over defaults", Budget{}, Budget{Context: 500, Turns: 30}},
		{"step over binding", Budget{Context: 100, Turns: 4}, Budget{Context: 100, Turns: 4}},
		{"step sets one bound", Budget{Turns: 4}, Budget{Context: 500, Turns: 4}},
	}
	for _, c := range cases {
		if got := ac.BudgetFor(b, c.step); got != c.want {
			t.Errorf("%s: BudgetFor = %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := (AgentsConfig{}).BudgetFor(AgentBinding{}, Budget{}); got.Set() {
		t.Errorf("no tier sets a budget: nothing may be invented, got %+v", got)
	}
}

// TestNegativeBudgetRefused: a negative bound fails at load, on a binding and on
// [defaults], naming the key.
func TestNegativeBudgetRefused(t *testing.T) {
	for _, body := range []string{
		"[coder]\nrole = \"agent\"\ncommand = \"claude -p\"\nturn_budget = -1\n",
		"[coder]\nrole = \"agent\"\ncommand = \"claude -p\"\ncontext_budget = -5\n",
		"[defaults]\nturn_budget = -2\n",
		"[defaults]\ncontext_budget = -2\n",
	} {
		_, err := loadAgentsBody(body)
		if err == nil || !strings.Contains(err.Error(), "_budget") || !strings.Contains(err.Error(), "negative") {
			t.Errorf("body %q: err = %v, want a negative-budget refusal", body, err)
		}
	}
}
