package verb_test

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// TestEventTelemetry pins the shared per-row reader (sty_43d228e4): each ledger
// kind yields exactly the agent-action fields it carries — reusing the cost view's
// payload models — and the outcome comes from the row's OWN kind (a review verdict
// is its own row, a telemetry envelope kind is its own outcome).
func TestEventTelemetry(t *testing.T) {
	mk := func(kind string, payload any) ledger.Entry {
		b, _ := json.Marshal(payload)
		return ledger.Entry{Kind: kind, Payload: b}
	}
	cases := []struct {
		name                    string
		e                       ledger.Entry
		agent, model, outcome   string
		tokensTotal, durationMs int64
	}{
		{
			name:  "agent_invocation carries cost, no outcome",
			e:     mk(ledger.KindAgentInvocation, map[string]any{"agent": "reviewer", "model": "sonnet", "tokens_total": 2000, "duration_ms": 2400}),
			model: "sonnet", agent: "reviewer", tokensTotal: 2000, durationMs: 2400,
		},
		{
			name:    "telemetry_event uses data.outcome + folds cost",
			e:       mk(ledger.KindTelemetryEvent, map[string]any{"kind": "agent-retry", "data": map[string]any{"outcome": "error", "tokens_total": 100, "duration_ms": 1500}}),
			outcome: "error", tokensTotal: 100, durationMs: 1500,
		},
		{
			name:    "telemetry_event with no data.outcome falls back to the envelope kind",
			e:       mk(ledger.KindTelemetryEvent, map[string]any{"kind": "agent-timeout", "data": map[string]any{}}),
			outcome: "agent-timeout",
		},
		{name: "review_accept is its own outcome", e: ledger.Entry{Kind: ledger.KindReviewAccept}, outcome: "accept"},
		{name: "review_reject is its own outcome", e: ledger.Entry{Kind: ledger.KindReviewReject}, outcome: "reject"},
		{name: "a plain status_transition carries nothing", e: mk(ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "plan"})},
	}
	for _, c := range cases {
		agent, model, outcome, tokens, dur := verb.EventTelemetry(c.e)
		if agent != c.agent || model != c.model || outcome != c.outcome || int64(tokens) != c.tokensTotal || dur != c.durationMs {
			t.Errorf("%s: EventTelemetry = (agent=%q model=%q outcome=%q tokens=%d dur=%d), want (agent=%q model=%q outcome=%q tokens=%d dur=%d)",
				c.name, agent, model, outcome, tokens, dur, c.agent, c.model, c.outcome, c.tokensTotal, c.durationMs)
		}
	}
}
