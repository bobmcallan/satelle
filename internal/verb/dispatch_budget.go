package verb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Spend budgets on a dispatched performer (sty_a7914904). The engine measures
// (agentstep.measureBudget); this file decides what a measurement leads to:
//
//   - no budget configured → warn and record; nothing is ever parked for spend;
//   - a budget the repo configured, exceeded → record a budget_overrun row and
//     route to rework when the step declares a rework loop, otherwise park the
//     story blocked with the reason kept on the ledger.
//
// The numbers are the repo's. Go ships none.

// Budget kinds a report and an overrun row name.
const (
	BudgetKindContext = "context"
	BudgetKindTurns   = "turns"
)

// Consequences a budget_overrun row records.
const (
	OverrunRework  = "rework"
	OverrunBlocked = "blocked"
	// OverrunRefused: the step has no rework loop and the workflow offers no
	// blocked state to park in, so the transition is refused — never a status
	// satelle invented (satelle-agent-goals).
	OverrunRefused = "refused"
)

// BudgetOverrun is one bound a dispatch exceeded.
type BudgetOverrun struct {
	Kind     string `json:"kind"`
	Budget   int    `json:"budget"`
	Measured int    `json:"measured"`
}

// BudgetReport is what one dispatched performer run measured against the budget
// the repo resolved for it. Pointer fields are nil — with an adapter-named
// reason beside them — when the run did not report that figure: an absence is
// never a zero.
type BudgetReport struct {
	// ContextBudget / TurnBudget are the resolved bounds; 0 means unset.
	ContextBudget int `json:"context_budget,omitempty"`
	TurnBudget    int `json:"turn_budget,omitempty"`
	// TurnBudgetHandedToHarness: the binding's command carried {max_turns}, so
	// the harness itself was told the bound. When a turn budget is set and this
	// is false, TurnBudgetNote names the adapter and why.
	TurnBudgetHandedToHarness bool   `json:"turn_budget_handed_to_harness,omitempty"`
	TurnBudgetNote            string `json:"turn_budget_note,omitempty"`

	Turns                  *int   `json:"turns,omitempty"`
	TurnsUnavailableReason string `json:"turns_unavailable_reason,omitempty"`
	// ContextTokens is the run's input tokens: fresh + cache read + cache write.
	ContextTokens            *int   `json:"context_tokens,omitempty"`
	ContextUnavailableReason string `json:"context_unavailable_reason,omitempty"`

	Overruns []BudgetOverrun `json:"overruns,omitempty"`
}

// Set reports whether the repo configured any bound for this run.
func (r *BudgetReport) Set() bool { return r != nil && (r.ContextBudget > 0 || r.TurnBudget > 0) }

// BudgetOverrunPayload is the budget_overrun ledger row's payload.
type BudgetOverrunPayload struct {
	Agent       string `json:"agent"`
	Step        string `json:"step"`
	Kind        string `json:"kind"`
	Budget      int    `json:"budget"`
	Measured    int    `json:"measured"`
	Consequence string `json:"consequence"`
	Reason      string `json:"reason"`
}

// overrunReason is the one-line reason kept on the ledger and quoted when a
// story is parked.
func overrunReason(agent, step string, o BudgetOverrun) string {
	unit := "turns"
	if o.Kind == BudgetKindContext {
		unit = "input tokens"
	}
	return fmt.Sprintf("budget overrun: %s at step %s used %d %s > %s budget %d (configured by this repo)",
		agent, step, o.Measured, unit, o.Kind, o.Budget)
}

// applyDispatchBudget acts on a finished dispatch's report. It returns the
// notes to park the story with (non-empty means: park), and a refusal when the
// step needs parking but the workflow offers no blocked state. A nil or clean
// report returns ("", nil).
//
// resumePark is the park state the story would move to ("" when the workflow
// offers none); rework says whether the step declares a rework loop.
func applyDispatchBudget(ctx context.Context, item workitem.Item, step string, res DispatchResult, rework bool, resumePark string, now time.Time) (parkNotes string, refuse error) {
	r := res.Budget
	if r == nil {
		return "", nil
	}
	if !r.Set() {
		EmitVerdict(fmt.Sprintf(
			"satelle: no context_budget or turn_budget is configured for %s at step %s — %s; recorded only, nothing enforced",
			res.Agent, step, measuredSummary(r)))
		return "", nil
	}
	if r.TurnBudget > 0 && r.TurnBudgetNote != "" {
		EmitVerdict(fmt.Sprintf("satelle: turn_budget %d for %s: %s", r.TurnBudget, res.Agent, r.TurnBudgetNote))
	}
	var notes []string
	for _, o := range r.Overruns {
		reason := overrunReason(res.Agent, step, o)
		consequence := OverrunBlocked
		switch {
		case rework:
			consequence = OverrunRework
		case resumePark == "" || resumePark == step:
			consequence = OverrunRefused
		}
		appendLedgerEntry(ctx, item.ID, ledger.KindBudgetOverrun, "executor", reason,
			budgetJSON(BudgetOverrunPayload{Agent: res.Agent, Step: step, Kind: o.Kind, Budget: o.Budget,
				Measured: o.Measured, Consequence: consequence, Reason: reason}), now)
		switch consequence {
		case OverrunRework:
			EmitVerdict(fmt.Sprintf("satelle: %s — step %s declares a rework loop: run `satelle story rework %s` to converge the slice", reason, step, item.ID))
		case OverrunRefused:
			refuse = fmt.Errorf("%s; the step declares no rework loop and workflow offers no blocked state to park in", reason)
		default:
			EmitVerdict("satelle: " + reason + " — parking the story blocked")
			notes = append(notes, reason)
		}
	}
	if refuse != nil {
		return "", refuse
	}
	return strings.Join(notes, "; "), nil
}

// measuredSummary renders what a run measured, or why it could not say.
func measuredSummary(r *BudgetReport) string {
	part := func(n *int, unit, why string) string {
		if n == nil {
			return "no " + unit + " reported (" + why + ")"
		}
		return fmt.Sprintf("%d %s", *n, unit)
	}
	return "measured " + part(r.Turns, "turns", r.TurnsUnavailableReason) + ", " +
		part(r.ContextTokens, "input tokens", r.ContextUnavailableReason)
}
