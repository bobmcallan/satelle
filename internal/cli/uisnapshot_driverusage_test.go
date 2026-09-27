package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestListSeatsJSONRecordsKillRowOnReap pins the THIRD AC5/AC6 wiring point
// named by the review-round-3 fix, alongside story-seat-list
// (internal/verb/seat.go) and acquireEngagementLease's steal path
// (internal/verb/single_story.go): the web-push mirror's OWN seat listing
// (listSeatsJSON) reaps dead leases straight off app.App.Store, bypassing the
// verb dispatch seam entirely — an operator who only ever looks at the web UI
// and never runs `satelle story seat` would otherwise never get a
// driver_usage row for a session this call reaps (sty_81caa41b).
func TestListSeatsJSONRecordsKillRowOnReap(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verb.SetLedgerStore(db.Ledger)
	t.Cleanup(func() { verb.SetLedgerStore(nil) })

	ctx := context.Background()
	sty, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Dispatch Story", Body: "goal",
		AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusInProgress,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: sty.ID, Kind: "story", Owner: "test@local", State: "in_progress",
		StorySeat: true, SessionID: "sess-du-webseat",
	}); err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("acquire lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, sty.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	a := &app.App{Config: config.Config{}, RepoRoot: repo, Store: db}
	if _, err := listSeatsJSON(ctx, a); err != nil {
		t.Fatalf("listSeatsJSON: %v", err)
	}

	entries, err := db.Ledger.ListByStory(ctx, sty.ID, ledger.KindDriverUsage)
	if err != nil {
		t.Fatalf("ListByStory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("driver_usage rows = %d, want 1 (the reaped lease's kill row): %+v", len(entries), entries)
	}
	var payload struct {
		Trigger string `json:"trigger"`
	}
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Trigger != "kill" {
		t.Fatalf("trigger = %q, want %q", payload.Trigger, "kill")
	}

	// Reaping again (the lease is gone) must not write a second row for the
	// same story — nothing left to reap.
	if _, err := listSeatsJSON(ctx, a); err != nil {
		t.Fatalf("listSeatsJSON (second call): %v", err)
	}
	entries, err = db.Ledger.ListByStory(ctx, sty.ID, ledger.KindDriverUsage)
	if err != nil {
		t.Fatalf("ListByStory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("driver_usage rows after second reap = %d, want still 1: %+v", len(entries), entries)
	}
}
