package agentstep

import (
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// measureBudget reports what one dispatched performer run measured against the
// budget the repo resolved for it (sty_a7914904). It is mechanism only: it
// records the figures, what the adapter could and could not do, and any bound
// exceeded — the verb layer alone decides what an overrun leads to.
//
// A figure the run did not report is recorded unavailable with an adapter-named
// reason and is NEVER read as zero, so an unmeasured run cannot look like an
// overrun (satelle-agent-agnostic §2). With no budget configured the report
// still carries the measurements: satelle warns and records, and never parks.
func measureBudget(binding config.AgentBinding, budget config.Budget, u agentcli.UsageResult) *verb.BudgetReport {
	iface, command := binding.ResolvedInterface(), binding.CommandTemplate()
	r := &verb.BudgetReport{ContextBudget: budget.Context, TurnBudget: budget.Turns}

	if budget.Turns > 0 {
		if sup := agentcli.TurnBudgetSupport(iface, command); sup.Available {
			r.TurnBudgetHandedToHarness = true
		} else {
			r.TurnBudgetNote = sup.Reason
		}
	}

	if u.TurnsAvailable {
		n := u.Turns
		r.Turns = &n
		if budget.Turns > 0 && n > budget.Turns {
			r.Overruns = append(r.Overruns, verb.BudgetOverrun{Kind: verb.BudgetKindTurns, Budget: budget.Turns, Measured: n})
		}
	} else {
		r.TurnsUnavailableReason = agentcli.TurnsUnavailableReason(iface, command)
	}

	if u.Available {
		n := u.InputTokens
		r.ContextTokens = &n
		if budget.Context > 0 && n > budget.Context {
			r.Overruns = append(r.Overruns, verb.BudgetOverrun{Kind: verb.BudgetKindContext, Budget: budget.Context, Measured: n})
		}
	} else {
		r.ContextUnavailableReason = u.UnavailableReason
		if r.ContextUnavailableReason == "" {
			r.ContextUnavailableReason = agentcli.AdapterLabel(iface, command) + ": the run reported no token usage"
		}
	}
	return r
}
