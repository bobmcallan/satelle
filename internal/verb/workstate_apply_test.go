package verb_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestWorkstateApplierFiresOnCreate(t *testing.T) {
	wire(t)
	t.Cleanup(verb.ClearWorkstateApplier)
	var n atomic.Int32
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		n.Add(1)
		if len(items) != 1 || items[0].Title != "A" {
			t.Errorf("items = %+v", items)
		}
	})
	call(t, "story-create", map[string]any{"title": "A"})
	if n.Load() != 1 {
		t.Fatalf("applier fired %d times", n.Load())
	}
}

func TestWorkstateApplierFiresOnLedgerAppend(t *testing.T) {
	wire(t)
	t.Cleanup(verb.ClearWorkstateApplier)
	var n atomic.Int32
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		if len(entries) == 1 {
			n.Add(1)
		}
	})
	call(t, "ledger-append", map[string]any{"kind": "note", "body": "x", "story_id": "sty_x"})
	if n.Load() != 1 {
		t.Fatalf("ledger applier fired %d", n.Load())
	}
}

func TestWorkstateApplierFiresOnStorySet(t *testing.T) {
	wire(t)
	t.Cleanup(verb.ClearWorkstateApplier)
	var n atomic.Int32
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		n.Add(1)
	})
	var created workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "SetMe"}), &created); err != nil {
		t.Fatal(err)
	}
	before := n.Load()
	call(t, "story-set", map[string]any{"id": created.ID, "status": "in_progress"})
	if n.Load() <= before {
		t.Fatalf("story-set did not fire applier (before=%d after=%d)", before, n.Load())
	}
}

func TestWorkstateApplierNilIsInert(t *testing.T) {
	wire(t)
	verb.ClearWorkstateApplier()
	var created workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "B"}), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("create failed with nil applier")
	}
}
