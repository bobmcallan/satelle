package storyfold

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func seedLedger(t *testing.T, name string) *ledger.Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ledger.Migrate(db); err != nil {
		t.Fatal(err)
	}
	led := ledger.New(db)
	led.SetWorkflowSource(func(context.Context, ledger.Conn, string) ledger.WorkflowSource {
		return ledger.WorkflowSource{Hash: "h1", Legacy: "workflow body"}
	})
	return led
}

// SeededEvents names a chain's local-only events when they are the genesis SeedItem
// makes — its workflow_version and definition_snapshot — and refuses anything else,
// or a chain with nothing local-only.
func TestSeededEvents(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	led := seedLedger(t, "seededevents")
	it := workitem.Item{ID: "sty_seed", Kind: workitem.KindStory, Title: "t", Status: "backlog"}
	if added, err := SeedItem(ctx, led, it, StatusFromChain, now); err != nil || !added {
		t.Fatalf("seed = %v, %v", added, err)
	}
	chain, err := led.StoryChain(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || !ledger.IsVersionEvent(chain[0]) || chain[1].Kind != ledger.KindDefinitionSnapshot {
		t.Fatalf("seeded chain = %+v, want a workflow_version then a definition_snapshot", chain)
	}
	all := map[string]bool{chain[0].ID: true, chain[1].ID: true}

	ids, ok := SeededEvents(chain, all)
	if !ok || len(ids) != 2 || ids[0] != chain[0].ID || ids[1] != chain[1].ID {
		t.Fatalf("SeededEvents = %v, %v; want both events in chain order", ids, ok)
	}
	if _, ok := SeededEvents(chain, map[string]bool{}); ok {
		t.Error("a chain with nothing local-only was reported seeded")
	}
	if _, ok := SeededEvents(chain, map[string]bool{chain[0].ID: true}); ok {
		t.Error("a workflow_version alone, with no snapshot, was reported seeded")
	}

	// Real work after the genesis is never a seed: a human transition, and a
	// snapshot an actor other than the binary made.
	work, err := led.Append(ctx, ledger.AppendInput{StoryID: it.ID, Kind: ledger.KindStatusTransition, Actor: "bob"}, now)
	if err != nil {
		t.Fatal(err)
	}
	chain = append(chain, work)
	all[work.ID] = true
	if _, ok := SeededEvents(chain, all); ok {
		t.Error("a chain with a transition among its local-only events was reported seeded")
	}
	if IsSeededEvent(ledger.Entry{StoryID: it.ID, Kind: ledger.KindDefinitionSnapshot, Actor: "bob", Refs: chain[1].Refs}) {
		t.Error("a snapshot made by a person was reported seeded")
	}
}
