package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_41f61669 AC3: an appended event carries the content hash of the workflow
// governing its story, resolved through the handle the append runs on — a
// transition's append inside a transaction must neither deadlock on the
// single-connection pool nor lose the hash.
func TestLedgerAppendStampsGoverningWorkflowHash(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: hashwf\napplies_to: [\"*\"]\n---\n# hashwf\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "hashwf.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": dir}, time.Now().UTC()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	want, err := db.DocIndex.Get(ctx, "workflows", "hashwf")
	if err != nil || want.Hash == "" {
		t.Fatalf("seeded workflow = %+v, %v", want, err)
	}

	stamped, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "stamped", Tags: []string{"workflow:hashwf"},
	}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: stamped.ID, Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if e.WorkflowHash != want.Hash {
		t.Errorf("hash = %q, want the workflow doc hash %q", e.WorkflowHash, want.Hash)
	}

	// Inside a transaction, as a status transition appends.
	var inTx ledger.Entry
	err = db.InTx(ctx, func(tx *sql.Tx) error {
		var aerr error
		inTx, aerr = db.Ledger.WithTx(tx).Append(ctx, ledger.AppendInput{StoryID: stamped.ID, Kind: "noise"}, time.Time{})
		return aerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if inTx.WorkflowHash != want.Hash || inTx.BaseID != e.ID {
		t.Errorf("in-tx entry = base %q hash %q, want base %q hash %q", inTx.BaseID, inTx.WorkflowHash, e.ID, want.Hash)
	}

	// An explicit hash is kept; an unknown story gets none.
	given, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: stamped.ID, Kind: "noise", WorkflowHash: "explicit"}, time.Time{})
	if err != nil || given.WorkflowHash != "explicit" {
		t.Errorf("explicit hash = %q, %v", given.WorkflowHash, err)
	}
	ghost, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_unknown", Kind: "noise"}, time.Time{})
	if err != nil || ghost.WorkflowHash != "" {
		t.Errorf("unknown story hash = %q, %v", ghost.WorkflowHash, err)
	}
	if rep, err := db.Ledger.VerifyAll(ctx); err != nil || len(rep.Breaks) != 0 {
		t.Errorf("chain = %+v, %v", rep, err)
	}
}
