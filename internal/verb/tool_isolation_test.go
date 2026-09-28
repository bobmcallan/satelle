package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestGateInvocationRowCarriesToolIsolation pins sty_ef3efb51 AC5: the
// reviewer's agent_invocation row records the offered tool count with its
// source, and the adapter-named limitation, alongside system_prompt_bytes —
// and ledger.EventTelemetry reads them back. A count is nil (no key) where the
// adapter has none, never a measured zero.
func TestGateInvocationRowCarriesToolIsolation(t *testing.T) {
	three := 3
	cases := []struct {
		name      string
		iso       verb.ToolIsolation
		wantCount *int
		wantKey   bool
	}{
		{"flag-sourced count", verb.ToolIsolation{OfferedToolCount: &three, OfferedToolsSource: "flag"}, &three, true},
		{"harness-reported count", verb.ToolIsolation{OfferedToolCount: &three, OfferedToolsSource: "harness"}, &three, true},
		{"grok acp: unavailable, limitation, no number", verb.ToolIsolation{
			OfferedToolsSource:  "unavailable: grok agent stdio neither trims nor reports offered tools",
			IsolationLimitation: "grok/acp: grok agent stdio cannot trim the offered tools",
		}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := wire(t)
			verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
				Gated: true, Accept: true, Skill: "satelle-story-done-review", Notes: "n",
				Command: "claude -p", Context: "satelle-story-done-review",
				SystemPromptBytes: 1234, PayloadBytes: 56, ToolIsolation: tc.iso,
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
				t.Fatalf("want 1 agent_invocation row, got %d", len(entries))
			}
			if got := strings.Contains(string(entries[0].Payload), `"offered_tool_count"`); got != tc.wantKey {
				t.Errorf("offered_tool_count key present = %v, want %v: %s", got, tc.wantKey, entries[0].Payload)
			}
			tel := ledger.EventTelemetry(entries[0])
			if tel.SystemPromptBytes != 1234 {
				t.Errorf("system_prompt_bytes = %d, want 1234", tel.SystemPromptBytes)
			}
			if (tel.OfferedToolCount == nil) != (tc.wantCount == nil) || (tc.wantCount != nil && *tel.OfferedToolCount != *tc.wantCount) {
				t.Errorf("offered_tool_count = %v, want %v", tel.OfferedToolCount, tc.wantCount)
			}
			if tel.OfferedToolsSource != tc.iso.OfferedToolsSource || tel.IsolationLimitation != tc.iso.IsolationLimitation {
				t.Errorf("source/limitation = %q/%q, want %q/%q", tel.OfferedToolsSource, tel.IsolationLimitation, tc.iso.OfferedToolsSource, tc.iso.IsolationLimitation)
			}
		})
	}
}
