package verb_test

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// TestEventTelemetry pins the shared per-row reader (sty_43d228e4 / sty_56aae77a):
// each ledger kind yields exactly the agent-action fields it carries, and usage
// availability is inferred once in ledger — never re-derived by consumers.
func TestEventTelemetry(t *testing.T) {
	mk := func(kind string, payload any) ledger.Entry {
		b, _ := json.Marshal(payload)
		return ledger.Entry{Kind: kind, Payload: b}
	}
	trueV, falseV := true, false
	cases := []struct {
		name string
		e    ledger.Entry
		want ledger.Telemetry
	}{
		{
			name: "agent_invocation with usage_available true",
			e: mk(ledger.KindAgentInvocation, map[string]any{
				"agent": "reviewer", "model": "sonnet",
				"tokens_in": 100, "tokens_out": 1900, "tokens_total": 2000, "duration_ms": 2400,
				"usage_available": true,
			}),
			want: ledger.Telemetry{
				Agent: "reviewer", Model: "sonnet",
				TokensIn: 100, TokensOut: 1900, TokensTotal: 2000, DurationMs: 2400,
				UsageAvailable: true,
			},
		},
		{
			name: "agent_invocation with usage_available false (measured silence)",
			e: mk(ledger.KindAgentInvocation, map[string]any{
				"agent": "reviewer", "tokens_total": 0, "usage_available": false,
			}),
			want: ledger.Telemetry{Agent: "reviewer", UsageAvailable: false},
		},
		{
			name: "legacy agent_invocation with tokens_total>0 infers measured",
			e: mk(ledger.KindAgentInvocation, map[string]any{
				"agent": "reviewer", "model": "sonnet", "tokens_total": 2000, "duration_ms": 2400,
			}),
			want: ledger.Telemetry{
				Agent: "reviewer", Model: "sonnet",
				TokensTotal: 2000, DurationMs: 2400, UsageAvailable: true,
			},
		},
		{
			name: "legacy agent_invocation with 0/0 is unreported",
			e: mk(ledger.KindAgentInvocation, map[string]any{
				"agent": "reviewer", "tokens_in": 0, "tokens_out": 0, "tokens_total": 0,
			}),
			want: ledger.Telemetry{Agent: "reviewer", UsageAvailable: false},
		},
		{
			name: "explicit measured zero stays measured",
			e: mk(ledger.KindAgentInvocation, map[string]any{
				"agent": "reviewer", "tokens_total": 0, "usage_available": true,
			}),
			want: ledger.Telemetry{Agent: "reviewer", UsageAvailable: true},
		},
		{
			name: "telemetry_event uses data.outcome + folds cost",
			e: mk(ledger.KindTelemetryEvent, map[string]any{
				"kind": "agent-retry",
				"data": map[string]any{"outcome": "error", "tokens_total": 100, "duration_ms": 1500},
			}),
			want: ledger.Telemetry{Outcome: "error", TokensTotal: 100, DurationMs: 1500, UsageAvailable: true},
		},
		{
			name: "telemetry_event with no data.outcome falls back to the envelope kind",
			e:    mk(ledger.KindTelemetryEvent, map[string]any{"kind": "agent-timeout", "data": map[string]any{}}),
			want: ledger.Telemetry{Outcome: "agent-timeout"},
		},
		{name: "review_accept is its own outcome", e: ledger.Entry{Kind: ledger.KindReviewAccept}, want: ledger.Telemetry{Outcome: "accept"}},
		{name: "review_reject is its own outcome", e: ledger.Entry{Kind: ledger.KindReviewReject}, want: ledger.Telemetry{Outcome: "reject"}},
		{name: "a plain status_transition carries nothing", e: mk(ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "plan"})},
		// pin pointer cases for compile clarity
		{name: "pointer true sentinel", e: mk(ledger.KindAgentInvocation, map[string]any{"usage_available": trueV}), want: ledger.Telemetry{UsageAvailable: true}},
		{name: "pointer false sentinel", e: mk(ledger.KindAgentInvocation, map[string]any{"usage_available": falseV}), want: ledger.Telemetry{UsageAvailable: false}},
	}
	for _, c := range cases {
		got := verb.EventTelemetry(c.e)
		if got != c.want {
			t.Errorf("%s: EventTelemetry = %+v, want %+v", c.name, got, c.want)
		}
	}
}
