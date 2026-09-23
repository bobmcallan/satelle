package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestReviewVerdictRowsCarryResolvedModel pins AC5: a review_accept and a
// review_reject row each carry model_resolved (and model_usage when present)
// DIRECTLY on the row's own payload — reviewerPayload (workitem.go:1395) —
// with no join to an agent_invocation entry, so the row stands alone after
// ledger compaction (sty_87b86044).
func TestReviewVerdictRowsCarryResolvedModel(t *testing.T) {
	db := wire(t)

	cases := []struct {
		name   string
		accept bool
		kind   string
	}{
		{"accept", true, ledger.KindReviewAccept},
		{"reject", false, ledger.KindReviewReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
				Gated: true, Accept: tc.accept, Skill: "satelle-story-done-review", Notes: "n",
				ModelResolved: "claude-opus-5-5",
				Models:        []verb.ModelUsage{{ID: "claude-opus-5-5", TokensIn: 10, TokensOut: 5}},
			}})
			t.Cleanup(func() { verb.SetTransitionGater(nil) })

			var it workitem.Item
			if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it); err != nil {
				t.Fatal(err)
			}
			_, _ = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

			entries, err := db.Ledger.ListByStory(context.Background(), it.ID, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("want 1 %s row, got %d: %+v", tc.kind, len(entries), entries)
			}
			var row struct {
				ModelResolved string            `json:"model_resolved"`
				Models        []verb.ModelUsage `json:"model_usage"`
			}
			if err := json.Unmarshal(entries[0].Payload, &row); err != nil {
				t.Fatal(err)
			}
			if row.ModelResolved != "claude-opus-5-5" {
				t.Errorf("model_resolved = %q, want claude-opus-5-5", row.ModelResolved)
			}
			if len(row.Models) != 1 || row.Models[0].ID != "claude-opus-5-5" || row.Models[0].TokensOut != 5 {
				t.Errorf("model_usage = %+v", row.Models)
			}
			// The row stands alone: ledger.EventTelemetry reads model_resolved off
			// THIS row's own payload, without consulting any agent_invocation entry.
			tel := ledger.EventTelemetry(entries[0])
			if tel.ModelResolved != "claude-opus-5-5" {
				t.Errorf("EventTelemetry.ModelResolved = %q, want claude-opus-5-5", tel.ModelResolved)
			}
		})
	}
}

// TestReviewVerdictRowCarriesAliasWhenUnresolved pins AC6: when the gate
// reports a configured alias but no resolved id, review_accept/review_reject
// rows still carry the alias (reviewerPayload's "model" field, workitem.go),
// so ledger.ModelLabel renders "<alias> (unknown)" rather than a bare
// "unknown" — the web chip must not lose the alias just because resolution
// failed (sty_87b86044).
func TestReviewVerdictRowCarriesAliasWhenUnresolved(t *testing.T) {
	db := wire(t)

	cases := []struct {
		name   string
		accept bool
		kind   string
	}{
		{"accept", true, ledger.KindReviewAccept},
		{"reject", false, ledger.KindReviewReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
				Gated: true, Accept: tc.accept, Skill: "satelle-story-done-review", Notes: "n",
				Model: "opus",
			}})
			t.Cleanup(func() { verb.SetTransitionGater(nil) })

			var it workitem.Item
			if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it); err != nil {
				t.Fatal(err)
			}
			_, _ = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

			entries, err := db.Ledger.ListByStory(context.Background(), it.ID, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("want 1 %s row, got %d: %+v", tc.kind, len(entries), entries)
			}
			var row struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(entries[0].Payload, &row); err != nil {
				t.Fatal(err)
			}
			if row.Model != "opus" {
				t.Errorf("model = %q, want opus", row.Model)
			}
			tel := ledger.EventTelemetry(entries[0])
			if got := ledger.ModelLabel(tel.Model, tel.ModelResolved); got != "opus (unknown)" {
				t.Errorf("ModelLabel = %q, want %q", got, "opus (unknown)")
			}
		})
	}
}

// TestReviewVerdictRowLegacyRendersUnknown pins AC7 for the verdict-row
// surface: a review_accept row written before this change (no model_resolved
// key at all) decodes without error and renders "unknown" — never
// back-filled, never an error.
func TestReviewVerdictRowLegacyRendersUnknown(t *testing.T) {
	legacy := ledger.Entry{
		Kind:    ledger.KindReviewAccept,
		Payload: json.RawMessage(`{"from":"plan","to":"in_progress","skill":"satelle-story-plan-review","accept":true}`),
	}
	tel := ledger.EventTelemetry(legacy)
	if tel.ModelResolved != "" {
		t.Fatalf("legacy row should decode with ModelResolved empty, got %q", tel.ModelResolved)
	}
	if got := ledger.ModelLabel(tel.Model, tel.ModelResolved); got != "unknown" {
		t.Errorf("ModelLabel(legacy) = %q, want unknown", got)
	}
	// Byte-identical: rendering must never rewrite the stored row.
	want := `{"from":"plan","to":"in_progress","skill":"satelle-story-plan-review","accept":true}`
	if string(legacy.Payload) != want {
		t.Fatalf("legacy payload mutated: %s", legacy.Payload)
	}
}
