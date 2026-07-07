package verb_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// costSummariser returns a recap that carries its OWN billable invocation
// (Command + usage set), so recordStepSummary takes the branch that folds the
// summariser's cost into an agent_invocation row (AC3, sty_b73c3236).
type costSummariser struct{}

func (costSummariser) Summarise(_ context.Context, _ workitem.Item, _, _ string) (verb.SummaryResult, error) {
	return verb.SummaryResult{
		Text: "the recap", Command: "claude -p", Context: "satelle-step-summary", Model: "sonnet",
		TokensIn: 1200, TokensOut: 800, TokensTotal: 2000, DurationMs: 4200,
	}, nil
}
func (costSummariser) MandatorySummary(_ context.Context, _ workitem.Item) bool { return true }

// TestSummariserCostFoldsIntoAgentInvocation pins AC3 (sty_b73c3236): when the
// summariser reports its own cost (SummaryResult.Command set), recordStepSummary
// writes a KindAgentInvocation row so the summariser's usage — previously a
// documented gap (sty_a699ad14) — folds into `satelle story cost`. Driven through
// the `story-resummarise` verb, which shares recordStepSummary's write path.
func TestSummariserCostFoldsIntoAgentInvocation(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	verb.SetStepSummariser(costSummariser{})
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
		verb.SetStepSummariser(nil)
	})

	var st workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Cost", "acceptance_criteria": "1. ok"}), &st)

	// Re-run the summariser for an edge — this invokes recordStepSummary.
	call(t, "story-resummarise", map[string]any{"id": st.ID, "from": "plan", "to": "in_progress"})

	// The summariser's own cost is on the ledger as an agent_invocation row.
	var inv []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": st.ID, "kind": ledger.KindAgentInvocation}), &inv)
	if len(inv) != 1 {
		t.Fatalf("want exactly 1 summariser agent_invocation row, got %d", len(inv))
	}
	if inv[0].Actor != "reviewer" {
		t.Errorf("summariser invocation actor = %q, want reviewer", inv[0].Actor)
	}

	// …and it counts in `satelle story cost` (the gap this closes).
	cost, err := verb.ComputeStoryCost(context.Background(), st.ID)
	if err != nil {
		t.Fatalf("story cost: %v", err)
	}
	if cost.TotalTokens < 2000 {
		t.Errorf("summariser's 2000 tokens should fold into the story cost total, got %d", cost.TotalTokens)
	}
}

// TestSummariserNoCostRowWhenNoCommand is the negative: a recap with no billable
// invocation (Command empty — e.g. a stubbed/local summariser) records the
// step_summary text but writes NO agent_invocation cost row.
func TestSummariserNoCostRowWhenNoCommand(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	verb.SetStepSummariser(stubSummariser{out: "recap only", mandatory: true})
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
		verb.SetStepSummariser(nil)
	})

	var st workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "NoCost", "acceptance_criteria": "1. ok"}), &st)
	call(t, "story-resummarise", map[string]any{"id": st.ID, "from": "plan", "to": "in_progress"})

	var inv []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": st.ID, "kind": ledger.KindAgentInvocation}), &inv)
	if len(inv) != 0 {
		t.Errorf("a summariser with no billable command must write no agent_invocation row, got %d", len(inv))
	}
}
