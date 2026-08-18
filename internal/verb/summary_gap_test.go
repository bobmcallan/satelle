package verb_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// stubSummariser is a fixed StepSummariser for the resummarise verb test.
type stubSummariser struct {
	out       string
	err       error
	mandatory bool
}

func (s stubSummariser) Summarise(_ context.Context, _ workitem.Item, _, _ string) (verb.SummaryResult, error) {
	return verb.SummaryResult{Text: s.out}, s.err
}
func (s stubSummariser) MandatorySummary(_ context.Context, _ workitem.Item) bool { return s.mandatory }

// TestStoryResummarise pins AC2's remediation (sty_a1151fb0): `story resummarise`
// re-runs the summariser for one edge and deposits the step-summary doc that closes
// the gap.
func TestStoryResummarise(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	verb.SetStepSummariser(stubSummariser{out: "the recovered recap", mandatory: true})
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTxRunner(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
		verb.SetStepSummariser(nil)
	})

	var st workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Gap", "acceptance_criteria": "1. ok"}), &st)

	// Re-run the summariser for the holed edge.
	out := call(t, "story-resummarise", map[string]any{"id": st.ID, "from": "plan", "to": "in_progress"})
	if !strings.Contains(string(out), `"resummarised":true`) {
		t.Fatalf("resummarise should report success: %s", out)
	}

	// The step-summary doc now exists and carries the recovered recap.
	got := call(t, "story-doc-get", map[string]any{"story_id": st.ID, "name": "step-summary-plan-in_progress"})
	if !strings.Contains(string(got), "the recovered recap") {
		t.Errorf("resummarise must deposit the step-summary doc: %s", got)
	}

	// Missing --from/--to is refused.
	raw, _ := json.Marshal(map[string]any{"id": st.ID})
	if _, err := verb.Dispatch(context.Background(), "story-resummarise", raw); err == nil {
		t.Error("resummarise without --from/--to must be refused")
	}
}
