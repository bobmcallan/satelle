package cli

import (
	"context"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestSessionLedgerWriteInvocationWritesToolPermissionKind pins sty_8eae81ac
// AC4: a tool-permission decision from a live rework-relay session lands under
// its own ledger kind, not agent_invocation — so it never inflates a cost
// total or an unmeasured-row count.
func TestSessionLedgerWriteInvocationWritesToolPermissionKind(t *testing.T) {
	tempRepo(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sl := &storeSessionLedger{ctx: context.Background(), storyID: "sty_perm1", ls: db.Ledger, actor: "coder"}
	if err := sl.WriteInvocation("Edit", "permission", "allow", "policy"); err != nil {
		t.Fatal(err)
	}

	entries, err := db.Ledger.ListByStory(context.Background(), "sty_perm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != ledger.KindToolPermission {
		t.Errorf("kind = %q, want %q", e.Kind, ledger.KindToolPermission)
	}
	if !ledger.IsToolPermissionRow(e) {
		t.Error("IsToolPermissionRow must recognise a freshly written row")
	}
	if e.Kind == ledger.KindAgentInvocation {
		t.Error("a tool-permission row must never land under agent_invocation")
	}
}
