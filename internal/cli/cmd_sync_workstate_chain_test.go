package cli

import (
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// sty_41f61669 AC5: a ledger entry pushed through workstate sync keeps the base
// and workflow hash it was written with, so the pulling machine does not invent a
// local tail for it.
func TestWorkstateLedgerCodecKeepsBaseAndWorkflowHash(t *testing.T) {
	in := ledger.Entry{
		ID: "evt_2", StoryID: "sty_a", Kind: ledger.KindComment, Actor: "executor", Body: "b",
		CreatedAt: time.Date(2026, 10, 23, 1, 2, 3, 0, time.UTC),
		BaseID:    "evt_1", WorkflowHash: "wf-hash",
	}
	raw, err := marshalWorkstateLedger(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseWorkstateLedger(hosted.WorkstateLedgerRow{ID: in.ID, StoryID: in.StoryID, Kind: in.Kind, Record: raw})
	if err != nil {
		t.Fatal(err)
	}
	if out.BaseID != "evt_1" || out.WorkflowHash != "wf-hash" {
		t.Errorf("round trip = base %q hash %q, want evt_1 / wf-hash", out.BaseID, out.WorkflowHash)
	}
	if out.ID != in.ID || out.Body != in.Body || !out.CreatedAt.Equal(in.CreatedAt) {
		t.Errorf("round trip changed the entry: %+v vs %+v", out, in)
	}

	// A root event, and a row from a machine that predates the fields, arrive baseless.
	root := ledger.Entry{ID: "evt_1", StoryID: "sty_a", Kind: ledger.KindComment, CreatedAt: in.CreatedAt}
	raw, err = marshalWorkstateLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseWorkstateLedger(hosted.WorkstateLedgerRow{ID: root.ID, StoryID: root.StoryID, Kind: root.Kind, Record: raw})
	if err != nil || got.BaseID != "" || got.WorkflowHash != "" {
		t.Errorf("root = %+v, %v", got, err)
	}
}
