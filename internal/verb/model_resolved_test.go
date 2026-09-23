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
// review_reject row each carry model_resolved/model_source (and model_usage
// when present) DIRECTLY on the row's own payload — reviewerPayload
// (workitem.go:1395) — with no join to an agent_invocation entry, so the row
// stands alone after ledger compaction (sty_87b86044 / sty_7069bced). This is
// the single-reviewer path (no dec.Reviewers): GateDecision.ModelSource must
// survive the synthesis into a ReviewerVerdict before it reaches the row.
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
				ModelResolved: "claude-opus-5-5", ModelSource: "inherited-in-loop",
				Models: []verb.ModelUsage{{ID: "claude-opus-5-5", TokensIn: 10, TokensOut: 5}},
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
				ModelSource   string            `json:"model_source"`
				Models        []verb.ModelUsage `json:"model_usage"`
			}
			if err := json.Unmarshal(entries[0].Payload, &row); err != nil {
				t.Fatal(err)
			}
			if row.ModelResolved != "claude-opus-5-5" {
				t.Errorf("model_resolved = %q, want claude-opus-5-5", row.ModelResolved)
			}
			if row.ModelSource != "inherited-in-loop" {
				t.Errorf("model_source = %q, want inherited-in-loop", row.ModelSource)
			}
			if len(row.Models) != 1 || row.Models[0].ID != "claude-opus-5-5" || row.Models[0].TokensOut != 5 {
				t.Errorf("model_usage = %+v", row.Models)
			}
			// The row stands alone: ledger.EventTelemetry reads model_resolved and
			// model_source off THIS row's own payload, without consulting any
			// agent_invocation entry.
			tel := ledger.EventTelemetry(entries[0])
			if tel.ModelResolved != "claude-opus-5-5" {
				t.Errorf("EventTelemetry.ModelResolved = %q, want claude-opus-5-5", tel.ModelResolved)
			}
			if tel.ModelSource != "inherited-in-loop" {
				t.Errorf("EventTelemetry.ModelSource = %q, want inherited-in-loop", tel.ModelSource)
			}
		})
	}
}

// TestGateInvocationRowCarriesModelSource pins the reviewer's OWN
// agent_invocation row (invocationPayload, workitem.go:1523 — Agent
// "reviewer", written whenever a verdict carries a Command): model_source
// round-trips through ledger.EventTelemetry the same as model_resolved does,
// off the row invocationPayload stamps, not the review_accept row
// reviewerPayload stamps (two different payload writers, sty_7069bced AC5).
func TestGateInvocationRowCarriesModelSource(t *testing.T) {
	db := wire(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-story-done-review", Notes: "n",
		Command: "claude -p", Context: "satelle-story-done-review",
		ModelResolved: "claude-opus-5-5", ModelSource: "binding",
	}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it); err != nil {
		t.Fatal(err)
	}
	_, _ = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 agent_invocation row, got %d: %+v", len(entries), entries)
	}
	if entries[0].Actor != "reviewer" {
		t.Fatalf("actor = %q, want reviewer", entries[0].Actor)
	}
	tel := ledger.EventTelemetry(entries[0])
	if tel.ModelResolved != "claude-opus-5-5" {
		t.Errorf("EventTelemetry.ModelResolved = %q, want claude-opus-5-5", tel.ModelResolved)
	}
	if tel.ModelSource != "binding" {
		t.Errorf("EventTelemetry.ModelSource = %q, want binding", tel.ModelSource)
	}
}

// TestDispatchInvocationRowCarriesModelSource pins a named-executor dispatch's
// agent_invocation row (dispatchPayload, workitem.go:1463 — Actor "executor"):
// model_source round-trips through a real ledger.Store and
// ledger.EventTelemetry the same as the reviewer/summariser rows do
// (sty_7069bced AC5).
func TestDispatchInvocationRowCarriesModelSource(t *testing.T) {
	db := wire(t)
	d := &dispatcherStub{res: verb.DispatchResult{
		Dispatched: true, Agent: "architect", Command: "fake {system}",
		Model: "m", ModelResolved: "claude-sonnet-5", ModelSource: "step",
	}}
	verb.SetExecutorDispatcher(d)
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}); err != nil {
		t.Fatalf("dispatch success must enact: %v", err)
	}

	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 agent_invocation row, got %d: %+v", len(entries), entries)
	}
	if entries[0].Actor != "executor" {
		t.Fatalf("actor = %q, want executor", entries[0].Actor)
	}
	tel := ledger.EventTelemetry(entries[0])
	if tel.ModelResolved != "claude-sonnet-5" {
		t.Errorf("EventTelemetry.ModelResolved = %q, want claude-sonnet-5", tel.ModelResolved)
	}
	if tel.ModelSource != "step" {
		t.Errorf("EventTelemetry.ModelSource = %q, want step", tel.ModelSource)
	}
}

// modelSourceSummariser is a StepSummariser whose result carries its own
// billable invocation (Command set) plus a ModelSource, so recordStepSummary
// takes the branch that writes an agent_invocation row via
// summariserInvocationPayload (workitem.go:1555).
type modelSourceSummariser struct{}

func (modelSourceSummariser) Summarise(context.Context, workitem.Item, string, string) (verb.SummaryResult, error) {
	return verb.SummaryResult{
		Text: "the recap", Command: "claude -p", Context: "satelle-step-summary",
		Model: "sonnet", ModelResolved: "claude-sonnet-5", ModelSource: "creator",
		UsageAvailable: true,
	}, nil
}
func (modelSourceSummariser) MandatorySummary(context.Context, workitem.Item) bool { return true }

// TestSummariserInvocationRowCarriesModelSource pins the step summariser's OWN
// agent_invocation row (summariserInvocationPayload, workitem.go:1555 — Agent
// "reviewer", written whenever the recap carries a Command): model_source
// round-trips through a real ledger.Store and ledger.EventTelemetry
// (sty_7069bced AC5) — driven through `story-resummarise`, which shares
// recordStepSummary's write path with the post-transition summariser.
func TestSummariserInvocationRowCarriesModelSource(t *testing.T) {
	db := wire(t)
	verb.SetStepSummariser(modelSourceSummariser{})
	t.Cleanup(func() { verb.SetStepSummariser(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "acceptance_criteria": "1. ok"}), &it)
	call(t, "story-resummarise", map[string]any{"id": it.ID, "from": "plan", "to": "in_progress"})

	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 agent_invocation row, got %d: %+v", len(entries), entries)
	}
	tel := ledger.EventTelemetry(entries[0])
	if tel.ModelResolved != "claude-sonnet-5" {
		t.Errorf("EventTelemetry.ModelResolved = %q, want claude-sonnet-5", tel.ModelResolved)
	}
	if tel.ModelSource != "creator" {
		t.Errorf("EventTelemetry.ModelSource = %q, want creator", tel.ModelSource)
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
