package ledger

import (
	"encoding/json"
	"testing"
)

// TestModelLabel pins the display rule shared by every surface (story route,
// story cost, the web timeline chip): a resolved canonical id wins, an
// alias-only row reads "<alias> (unknown)", and a row with neither (a legacy
// row, or an explicit "unavailable" marker) reads a bare "unknown" — never
// blank, never back-filled (sty_87b86044).
func TestModelLabel(t *testing.T) {
	cases := []struct {
		name           string
		alias, resolve string
		want           string
	}{
		{"resolved wins over alias", "opus", "claude-opus-5-5", "claude-opus-5-5"},
		{"alias only marks unknown", "opus", "", "opus (unknown)"},
		{"explicit unavailable marker reads like empty", "opus", "unavailable", "opus (unknown)"},
		{"neither recorded (legacy row)", "", "", "unknown"},
		{"resolved with no alias", "", "claude-opus-5-5", "claude-opus-5-5"},
	}
	for _, c := range cases {
		if got := ModelLabel(c.alias, c.resolve); got != c.want {
			t.Errorf("%s: ModelLabel(%q, %q) = %q, want %q", c.name, c.alias, c.resolve, got, c.want)
		}
	}
}

// TestEventTelemetry_ModelResolved pins ModelResolved's read path on the two
// row shapes that carry it: an agent_invocation payload (alongside the
// existing alias field) and a review_accept/review_reject verdict row (no
// join — the row stands alone). A row written before this field existed
// decodes with ModelResolved empty, never an error (sty_87b86044).
func TestEventTelemetry_ModelResolved(t *testing.T) {
	mk := func(kind string, payload any) Entry {
		b, _ := json.Marshal(payload)
		return Entry{Kind: kind, Payload: b}
	}
	cases := []struct {
		name string
		e    Entry
		want string
	}{
		{
			name: "agent_invocation carries both alias and resolved id",
			e: mk(KindAgentInvocation, map[string]any{
				"agent": "reviewer", "model": "opus", "model_resolved": "claude-opus-5-5",
			}),
			want: "claude-opus-5-5",
		},
		{
			name: "legacy agent_invocation has no model_resolved key",
			e: mk(KindAgentInvocation, map[string]any{
				"agent": "reviewer", "model": "opus",
			}),
			want: "",
		},
		{
			name: "review_accept carries model_resolved directly on the row",
			e:    mk(KindReviewAccept, map[string]any{"accept": true, "model_resolved": "claude-opus-5-5"}),
			want: "claude-opus-5-5",
		},
		{
			name: "review_reject carries model_resolved directly on the row",
			e:    mk(KindReviewReject, map[string]any{"accept": false, "model_resolved": "claude-opus-5-5"}),
			want: "claude-opus-5-5",
		},
		{
			name: "legacy review_accept predates the field",
			e:    mk(KindReviewAccept, map[string]any{"accept": true}),
			want: "",
		},
	}
	for _, c := range cases {
		got := EventTelemetry(c.e)
		if got.ModelResolved != c.want {
			t.Errorf("%s: ModelResolved = %q, want %q", c.name, got.ModelResolved, c.want)
		}
	}
}

// TestEventTelemetry_VerdictModelAlias pins that a review_accept/review_reject
// row's configured alias (the "model" field) decodes into Telemetry.Model
// alongside ModelResolved, so a verdict with no resolved id still renders
// "<alias> (unknown)" rather than a bare "unknown" on every surface that
// feeds ModelLabel (sty_87b86044).
func TestEventTelemetry_VerdictModelAlias(t *testing.T) {
	mk := func(kind string, payload any) Entry {
		b, _ := json.Marshal(payload)
		return Entry{Kind: kind, Payload: b}
	}
	cases := []struct {
		name  string
		e     Entry
		model string
		label string
	}{
		{
			name:  "review_accept with alias but no resolved id",
			e:     mk(KindReviewAccept, map[string]any{"accept": true, "model": "opus"}),
			model: "opus",
			label: "opus (unknown)",
		},
		{
			name:  "review_reject with alias but no resolved id",
			e:     mk(KindReviewReject, map[string]any{"accept": false, "model": "opus"}),
			model: "opus",
			label: "opus (unknown)",
		},
		{
			name:  "legacy verdict has neither model nor model_resolved",
			e:     mk(KindReviewAccept, map[string]any{"accept": true}),
			model: "",
			label: "unknown",
		},
	}
	for _, c := range cases {
		got := EventTelemetry(c.e)
		if got.Model != c.model {
			t.Errorf("%s: Model = %q, want %q", c.name, got.Model, c.model)
		}
		if label := ModelLabel(got.Model, got.ModelResolved); label != c.label {
			t.Errorf("%s: ModelLabel = %q, want %q", c.name, label, c.label)
		}
	}
}
