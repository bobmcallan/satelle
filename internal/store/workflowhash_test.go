package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const hashRouteDone = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled", gate = "cancel-review" }
`

const hashRouteStep = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["intent-review"]
requires = ["raised"]

[closed]
status = "done"
reviewers = ["done-review"]
terminal = true
requires = ["coded"]
`

// seedRoute writes done.toml + step.toml under a fresh workflows dir and indexes
// them, so the route they declare governs every story.
func seedRoute(t *testing.T, db *DB, done, step string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"done.toml": done, "step.toml": step} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": dir}, time.Now().UTC()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return dir
}

// wantVersion is the version wfgovern resolves over the indexed workflows for it.
func wantVersion(t *testing.T, db *DB, it workitem.Item) string {
	t.Helper()
	docs, err := db.DocIndex.ListIndexed(context.Background(), db.SQL(), "workflows")
	if err != nil {
		t.Fatal(err)
	}
	return wfgovern.RouteVersion(docs, it)
}

// sty_41f61669 AC3: an appended event carries the version of the route governing
// its story, resolved through the handle the append runs on — a transition's
// append inside a transaction must neither deadlock on the single-connection pool
// nor lose the hash.
func TestLedgerAppendStampsGoverningWorkflowHash(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	dir := seedRoute(t, db, hashRouteDone, hashRouteStep)

	story, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "stamped"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := wantVersion(t, db, story)
	if len(want) != 64 {
		t.Fatalf("seeded route version = %q, want a sha256 hex", want)
	}
	e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if e.WorkflowHash != want {
		t.Errorf("hash = %q, want the route version %q", e.WorkflowHash, want)
	}

	// Inside a transaction, as a status transition appends.
	var inTx ledger.Entry
	err = db.InTx(ctx, func(tx *sql.Tx) error {
		var aerr error
		inTx, aerr = db.Ledger.WithTx(tx).Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: ledger.KindStatusTransition}, time.Time{})
		return aerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if inTx.WorkflowHash != want || inTx.BaseID != e.ID {
		t.Errorf("in-tx entry = base %q hash %q, want base %q hash %q", inTx.BaseID, inTx.WorkflowHash, e.ID, want)
	}

	// An explicit hash is kept; an unknown story gets none.
	given, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: "noise", WorkflowHash: "explicit"}, time.Time{})
	if err != nil || given.WorkflowHash != "explicit" {
		t.Errorf("explicit hash = %q, %v", given.WorkflowHash, err)
	}
	ghost, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: "sty_unknown", Kind: "noise"}, time.Time{})
	if err != nil || ghost.WorkflowHash != "" {
		t.Errorf("unknown story hash = %q, %v", ghost.WorkflowHash, err)
	}

	// Editing a half of the route changes the version the next event carries.
	if err := os.WriteFile(filepath.Join(dir, "step.toml"), []byte(hashRouteStep+"\n# edited\n[extra]\nstatus = \"backlog\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": dir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if next.WorkflowHash == want || next.WorkflowHash != wantVersion(t, db, story) {
		t.Errorf("after editing step.toml hash = %q, old %q", next.WorkflowHash, want)
	}
	if rep, err := db.Ledger.VerifyAll(ctx); err != nil || len(rep.Breaks) != 0 {
		t.Errorf("chain = %+v, %v", rep, err)
	}
}
