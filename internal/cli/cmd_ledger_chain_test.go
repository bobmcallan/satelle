package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

func createChainStory(t *testing.T, title string) string {
	t.Helper()
	out, err := runRoot(t, "story", "create", "--title", title)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	return created.ID
}

// sty_41f61669 AC3: `ledger list --story` shows base_id and workflow_hash.
func TestLedgerListShowsChainFields(t *testing.T) {
	tempRepo(t)
	id := createChainStory(t, "chain fields")

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: id, Kind: "noise", WorkflowHash: "wf-hash-1"}, time.Now())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{StoryID: id, Kind: "noise", WorkflowHash: "wf-hash-2"}, time.Now()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	out, err := runRoot(t, "ledger", "list", "--story", id, "--kind", "noise")
	if err != nil {
		t.Fatalf("ledger list: %v\n%s", err, out)
	}
	for _, want := range []string{`"workflow_hash": "wf-hash-1"`, `"workflow_hash": "wf-hash-2"`, `"base_id": "` + first.ID + `"`} {
		if !strings.Contains(out, want) {
			t.Errorf("ledger list output missing %q:\n%s", want, out)
		}
	}
}

// sty_41f61669 AC6: verify exits 0 on a sound chain and non-zero on a broken one,
// names the break, and leaves the ledger as it found it.
func TestLedgerVerifyReportsBreak(t *testing.T) {
	tempRepo(t)
	id := createChainStory(t, "verify")
	other := createChainStory(t, "verify other")

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var es []ledger.Entry
	for i := 0; i < 3; i++ {
		e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: "noise"}, time.Now())
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		es = append(es, e)
	}
	db.Close()

	out, err := runRoot(t, "ledger", "verify")
	if err != nil || !strings.Contains(out, "chain ok") {
		t.Fatalf("a sound chain: err=%v\n%s", err, out)
	}
	if out, err := runRoot(t, "ledger", "verify", "--story", id); err != nil || !strings.Contains(out, "chain ok") {
		t.Fatalf("a sound story: err=%v\n%s", err, out)
	}

	db, err = store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE evidence SET base_id = 'evt_ghost' WHERE id = ?`, es[1].ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	out, err = runRoot(t, "ledger", "verify")
	if err == nil || ExitCodeOf(err) == 0 {
		t.Fatalf("a broken chain must exit non-zero, got %v\n%s", err, out)
	}
	if want := id + " " + es[1].ID + " missing_base:"; !strings.Contains(out, want) {
		t.Errorf("output missing %q:\n%s", want, out)
	}
	if out, err := runRoot(t, "ledger", "verify", "--story", id); err == nil {
		t.Errorf("the broken story must fail verify:\n%s", out)
	}
	if out, err := runRoot(t, "ledger", "verify", "--story", other); err != nil {
		t.Errorf("another story's chain is sound: %v\n%s", err, out)
	}

	db, err = store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, ok, err := db.Ledger.GetByID(ctx, es[1].ID)
	if err != nil || !ok || got.BaseID != "evt_ghost" {
		t.Errorf("verify repaired or changed the row: %+v ok=%v err=%v", got, ok, err)
	}
}
