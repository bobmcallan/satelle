package verb_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// wireLedgerOnly opens a store with just the stores PriorVerdicts reads, so the
// ledger→verdict read is exercised without a workflow in the way.
func wireLedgerOnly(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTxRunner(nil)
		verb.SetStoryDir("")
	})
}

// seedVerdict appends a reviewer row in the shape reviewerPayload writes.
func seedVerdict(t *testing.T, storyID, kind, from, to, skill, notes string) {
	t.Helper()
	call(t, "ledger-append", map[string]any{
		"story_id": storyID,
		"kind":     kind,
		"actor":    "reviewer",
		"body":     from + "→" + to + " by " + skill,
		"payload": map[string]any{
			"from": from, "to": to, "skill": skill, "order": 0,
			"notes": notes, "accept": kind == "review_accept",
		},
	})
}

// TestPriorVerdictsFiltersToTheEdge (sty_0f5e600c AC2/AC4): the read returns this
// story's verdicts on THIS edge, oldest first, and no other edge's.
func TestPriorVerdictsFiltersToTheEdge(t *testing.T) {
	wireLedgerOnly(t)
	ctx := context.Background()

	seedVerdict(t, "sty_pv1", "review_reject", "backlog", "plan", "satelle-story-intent-review", "OTHER-EDGE-NOTE")
	seedVerdict(t, "sty_pv1", "review_reject", "plan", "in_progress", "satelle-story-plan-review", "FIRST-EDGE-NOTE")
	seedVerdict(t, "sty_pv1", "review_accept", "plan", "in_progress", "satelle-story-plan-review", "SECOND-EDGE-NOTE")
	seedVerdict(t, "sty_pv1", "status_transition", "plan", "in_progress", "", "NOT-A-VERDICT")
	seedVerdict(t, "sty_other", "review_reject", "plan", "in_progress", "satelle-story-plan-review", "OTHER-STORY-NOTE")

	got, err := verb.PriorVerdicts(ctx, "sty_pv1", "plan", "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d verdicts, want 2: %+v", len(got), got)
	}
	if got[0].Notes != "FIRST-EDGE-NOTE" || got[0].Decision != "reject" {
		t.Errorf("first verdict = %+v, want the reject, oldest first", got[0])
	}
	if got[1].Notes != "SECOND-EDGE-NOTE" || got[1].Decision != "accept" {
		t.Errorf("second verdict = %+v, want the accept", got[1])
	}
	if got[0].Skill != "satelle-story-plan-review" {
		t.Errorf("skill = %q, want the judging skill", got[0].Skill)
	}
	if got[0].CreatedAt == "" {
		t.Error("created_at must be stamped from the ledger row")
	}

	// A first attempt on an untried edge reads as nothing at all.
	none, err := verb.PriorVerdicts(ctx, "sty_pv1", "in_progress", "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("untried edge returned %+v, want none", none)
	}
}

// TestPriorVerdictsWithoutLedgerIsInert (sty_0f5e600c): prior verdicts are
// additive context — an unwired ledger degrades to nothing, never an error that
// could fail the transition it decorates.
func TestPriorVerdictsWithoutLedgerIsInert(t *testing.T) {
	verb.SetLedgerStore(nil)
	got, err := verb.PriorVerdicts(context.Background(), "sty_none", "plan", "in_progress")
	if err != nil || got != nil {
		t.Fatalf("PriorVerdicts with no ledger = (%+v, %v), want (nil, nil)", got, err)
	}
}
