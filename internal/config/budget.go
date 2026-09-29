package config

import "fmt"

// Budget is a repo-authored spend bound (sty_a7914904). Both fields are token
// or turn COUNTS the operator chooses; zero means "not set", never "free" and
// never a shipped default — Go carries no number here. With no budget set,
// satelle only warns and records: nothing is ever parked for spend. A budget
// takes effect only because a repo configured one.
type Budget struct {
	// Context bounds the input a session or one dispatch may read: fresh +
	// cache-read + cache-write tokens, the same sum a driver_usage row carries.
	Context int
	// Turns bounds the model turns of one dispatched coder run. It is passed to
	// the harness where the adapter supports it (agentcli.TurnBudgetSupport) and
	// otherwise checked against the turns the harness reported.
	Turns int
}

// Set reports whether any bound is configured.
func (b Budget) Set() bool { return b.Context > 0 || b.Turns > 0 }

// Budget returns the bounds this binding declares (context_budget, turn_budget).
func (b AgentBinding) Budget() Budget {
	return Budget{Context: b.ContextBudget, Turns: b.TurnBudget}
}

// Budget returns the repo-wide bounds under [defaults].
func (d AgentsDefaults) Budget() Budget {
	return Budget{Context: d.ContextBudget, Turns: d.TurnBudget}
}

// ResolveBudget layers budget tiers, most specific first: for each bound the
// first tier that sets it wins. Callers pass the step's tier, then the binding's,
// then [defaults]. A tier that sets nothing is transparent.
func ResolveBudget(tiers ...Budget) Budget {
	var out Budget
	for _, t := range tiers {
		if out.Context == 0 {
			out.Context = t.Context
		}
		if out.Turns == 0 {
			out.Turns = t.Turns
		}
	}
	return out
}

// BudgetFor resolves the bounds that apply to a dispatch of binding b at a step
// declaring stepBudget: the step wins, then the binding, then [defaults].
func (a AgentsConfig) BudgetFor(b AgentBinding, stepBudget Budget) Budget {
	return ResolveBudget(stepBudget, b.Budget(), a.Defaults.Budget())
}

// checkBudget refuses a negative bound at load: a typo'd sign would otherwise
// read as "unset" and silently disable the bound the operator meant to set.
func checkBudget(where string, b Budget) error {
	if b.Context < 0 {
		return fmt.Errorf("%s context_budget %d: must not be negative (0 or absent means unset)", where, b.Context)
	}
	if b.Turns < 0 {
		return fmt.Errorf("%s turn_budget %d: must not be negative (0 or absent means unset)", where, b.Turns)
	}
	return nil
}
