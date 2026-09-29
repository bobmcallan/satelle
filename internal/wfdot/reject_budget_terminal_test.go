package wfdot

import "testing"

// sty_4cf2c585: a reject_budget on a TERMINAL step is carried onto the route, and
// a status several steps share (`done`) resolves against the category's OWN route,
// never the catalogue — so one category's budget cannot leak onto another's close.

const terminalBudgetDone = `["*"]
obligations = ["raised", "coded", "closed"]

[docs]
obligations = ["raised", "doc-authored", "docs-verified"]
`

const terminalBudgetSteps = `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
reviewers = ["gate-a"]
terminal = true
reject_budget = 2
requires = ["coded"]

[doc-authored]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[docs-verified]
status = "done"
reviewers = ["gate-b"]
terminal = true
requires = ["doc-authored"]
`

func TestTerminalStepRejectBudgetResolvesPerCategoryRoute(t *testing.T) {
	for category, want := range map[string]int{"feature": 2, "docs": 0} {
		spec, err := ParseRoute(terminalBudgetDone, terminalBudgetSteps, category, nil)
		if err != nil {
			t.Fatalf("%s: a budget on a terminal step must parse: %v", category, err)
		}
		done, ok := spec.StateNamed("done")
		if !ok {
			t.Fatalf("%s: route has no done state", category)
		}
		if done.RejectBudget != want {
			t.Errorf("%s: done reject_budget = %d, want %d (the route's own terminal step)", category, done.RejectBudget, want)
		}
	}
}
