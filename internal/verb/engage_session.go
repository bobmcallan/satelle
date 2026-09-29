package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Fresh-session advice at engage (sty_a7914904). A driving session that has
// already driven another story carries that story's context into this one, and
// every model call re-reads it. At the moment a story is engaged satelle says so
// once, on the engage output, and records the same on the ledger. It is a
// warning only: engage is never refused for it, and with no budget configured
// the other-story trigger still fires.

// Triggers a session_advisory row carries.
const (
	// SessionTriggerOtherStory: the session's driver_usage rows already name a
	// story other than the one being engaged.
	SessionTriggerOtherStory = "other-story"
	// SessionTriggerContextBudget: the session's cumulative input has reached the
	// context_budget the repo configured for this step.
	SessionTriggerContextBudget = "context-budget"
)

// SessionAdvisoryPayload is the session_advisory ledger row's payload.
type SessionAdvisoryPayload struct {
	SessionID string `json:"session_id"`
	Trigger   string `json:"trigger"`
	// OtherStories are the other stories the session's driver_usage rows cover.
	OtherStories []string `json:"other_stories,omitempty"`
	// ContextTokens is the session's cumulative input (fresh + cache read +
	// cache write) at its latest driver_usage row; ContextBudget the repo's bound.
	ContextTokens int    `json:"context_tokens,omitempty"`
	ContextBudget int    `json:"context_budget,omitempty"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
}

// agentBudgets resolves the repo's budget for a named agent binding (agent "" →
// [defaults] only): the binding's own bounds, then [defaults]. Wired by the CLI
// from agents.toml so the verb layer reads no config file.
var agentBudgets func(agent string) config.Budget

// SetAgentBudgets wires the per-agent budget resolver. Nil clears it, which
// leaves only a step's own budget in play.
func SetAgentBudgets(fn func(agent string) config.Budget) { agentBudgets = fn }

// resolveStepBudget is the budget that applies to a performer at step: the
// step's own declaration, then its allocated binding, then [defaults]. Zero
// fields are unset — nothing is inferred.
func resolveStepBudget(ctx context.Context, item workitem.Item, step string) config.Budget {
	idx, err := requireDocIndex()
	if err != nil {
		return config.Budget{}
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return config.Budget{}
	}
	spec, _, _, serr := wfgovern.SpecFor(wfs, item)
	if serr != nil {
		return config.Budget{}
	}
	st, ok := spec.StateNamed(step)
	if !ok {
		return config.Budget{}
	}
	stepB := config.Budget{Context: st.ContextBudget, Turns: st.TurnBudget}
	var agentB config.Budget
	if agentBudgets != nil {
		agentB = agentBudgets(st.Agent)
	}
	return config.ResolveBudget(stepB, agentB)
}

// stepDeclaresRework reports whether the item's governing route declares a
// rework loop on step — the fact the overrun consequence branches on.
func stepDeclaresRework(ctx context.Context, item workitem.Item, step string) bool {
	idx, err := requireDocIndex()
	if err != nil {
		return false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return false
	}
	d, _, rerr := wfgovern.RouteFor(wfs, item)
	if rerr != nil {
		return false
	}
	for _, w := range d.Reworks {
		if w.Step == step {
			return true
		}
	}
	return false
}

// checkFreshSession warns the engaging driver to start a fresh session when its
// own session already drove another story, or — when the repo configured a
// context budget for the step — has read that much. Called on entry into an
// engaging state from a non-engaging one. It never returns an error and never
// refuses: it appends a session_advisory row and prints the advice.
func checkFreshSession(ctx context.Context, item workitem.Item, from, to string, now time.Time) {
	if ledgerStore == nil || item.Kind != workitem.KindStory {
		return
	}
	clk, ok := clockFor(ctx, item)
	if !ok || !clk.Engaging(to) || clk.Engaging(from) {
		return
	}
	sessionID := config.ResolveSession()
	if strings.TrimSpace(sessionID) == "" {
		return // no driving session resolved: nothing to advise
	}
	var (
		others  []string
		seen    = map[string]bool{}
		latest  driverCumulative
		haveRow bool
	)
	_ = ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || p.SessionID != sessionID {
			return nil
		}
		if p.Available {
			latest, haveRow = p.Cumulative, true
		}
		if e.StoryID != "" && e.StoryID != item.ID && !seen[e.StoryID] {
			seen[e.StoryID] = true
			others = append(others, e.StoryID)
		}
		return nil
	})
	adv := SessionAdvisoryPayload{SessionID: sessionID, From: from, To: to}
	switch {
	case len(others) > 0:
		adv.Trigger, adv.OtherStories = SessionTriggerOtherStory, others
	case haveRow:
		// Only the repo's own bound can fire this trigger.
		budget := resolveStepBudget(ctx, item, to).Context
		used := latest.FreshInput + latest.CacheRead + latest.CacheWrite
		if budget <= 0 || used < budget {
			return
		}
		adv.Trigger, adv.ContextTokens, adv.ContextBudget = SessionTriggerContextBudget, used, budget
	default:
		return
	}
	line := freshSessionLine(item.ID, adv)
	appendLedgerEntry(ctx, item.ID, ledger.KindSessionAdvisory, "executor", line, budgetJSON(adv), now)
	EmitVerdict(line)
}

// freshSessionLine is the advice the driver reads at engage.
func freshSessionLine(itemID string, adv SessionAdvisoryPayload) string {
	if adv.Trigger == SessionTriggerContextBudget {
		return fmt.Sprintf(
			"satelle: this session has already read %d input tokens, at or past the repo's context_budget of %d — start a fresh session for %s (warning only; engage continues)",
			adv.ContextTokens, adv.ContextBudget, itemID)
	}
	return fmt.Sprintf(
		"satelle: this session already drove %s — start a fresh session for %s so it does not carry that context (warning only; engage continues)",
		strings.Join(adv.OtherStories, ", "), itemID)
}

// budgetJSON marshals a budget-family ledger payload; a marshal failure drops
// the payload, never the row.
func budgetJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
