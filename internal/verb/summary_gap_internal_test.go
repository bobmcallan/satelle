package verb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestSurfaceMissingSummaries pins AC2 (sty_a1151fb0): a story reaching done with a
// step-summary that was attempted (a KindStepSummary ledger row) but whose DOC is
// absent is surfaced as a NON-BLOCKING warning naming the edge + the exact
// remediation; once the doc exists the gap clears.
func TestSurfaceMissingSummaries(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	SetLedgerStore(db.Ledger)
	SetTxRunner(db.InTx)
	SetStoryDir(filepath.Join(dir, "stories"))
	t.Cleanup(func() {
		db.Close()
		SetLedgerStore(nil)
		SetTxRunner(nil)
		SetStoryDir("")
	})

	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	item := workitem.Item{ID: "sty_gap", Kind: workitem.KindStory}

	// A step-summary that FAILED on plan→in_progress (ledger row present, no doc).
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: item.ID, Kind: ledger.KindStepSummary, Actor: "reviewer",
		Body:    "step summary failed: signal: killed",
		Payload: transitionPayload("plan", "in_progress", ""),
	}, now); err != nil {
		t.Fatal(err)
	}

	surfaceMissingSummaries(ctx, item, now)

	countWarnings := func() int {
		entries, _ := db.Ledger.ListByStory(ctx, item.ID, ledger.KindStepSummary)
		n := 0
		for _, e := range entries {
			if strings.Contains(e.Body, "reached done with") &&
				strings.Contains(e.Body, "resummarise") &&
				strings.Contains(e.Body, "--from plan --to in_progress") {
				n++
			}
		}
		return n
	}
	if countWarnings() != 1 {
		t.Fatalf("expected exactly one surfaced gap warning naming the edge + remediation, got %d", countWarnings())
	}

	// Deposit the missing doc — the gap is now closed, so a rescan surfaces nothing new.
	if _, _, err := writeAttachedDoc(ctx, item, "step-summary-plan-in_progress", "step-summary", "the recap", now); err != nil {
		t.Fatal(err)
	}
	surfaceMissingSummaries(ctx, item, now)
	if countWarnings() != 1 {
		t.Errorf("gap should clear once the doc exists — want still exactly 1 warning total, got %d", countWarnings())
	}
}
