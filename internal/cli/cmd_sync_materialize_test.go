package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func testApp(t *testing.T) *app.App {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &app.App{Store: db}
}

func hostedStory(id, status string, updated time.Time) hosted.WorkstateItem {
	rec, _ := json.Marshal(workstateItemWire{
		ID: id, Kind: string(workitem.KindStory), Status: status,
		Title: "T", UpdatedAt: updated, CreatedAt: updated.Add(-time.Hour),
	})
	return hosted.WorkstateItem{ID: id, Kind: string(workitem.KindStory), Status: status, Title: "T", Record: rec}
}

func TestMaterializeSkipsStaleHostedRow(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	newer := created.Add(2 * time.Hour)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "T", Status: workitem.StatusDone,
	}, newer)
	if err != nil {
		t.Fatal(err)
	}
	// Force the timestamps the test reasons about (Create sets both to `newer`).
	if _, err := a.Store.Stories.SetStatus(ctx, it.ID, workitem.StatusDone, newer); err != nil {
		t.Fatal(err)
	}

	nItems, _, nKept, err := materializeWorkstate(ctx, a, map[string]bool{"stories": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, workitem.StatusBacklog, created)}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if nItems != 0 || nKept != 1 {
		t.Fatalf("applied=%d kept=%d, want applied=0 kept=1", nItems, nKept)
	}
	got, err := a.Store.Stories.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workitem.StatusDone {
		t.Errorf("row clobbered to %q", got.Status)
	}
}

func TestMaterializeForceOverwritesStaleRow(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	newer := created.Add(2 * time.Hour)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "T", Status: workitem.StatusDone,
	}, newer)
	if err != nil {
		t.Fatal(err)
	}
	nItems, _, nKept, err := materializeWorkstate(ctx, a, map[string]bool{"stories": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, workitem.StatusBacklog, created)}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if nItems != 1 || nKept != 0 {
		t.Fatalf("applied=%d kept=%d, want applied=1 kept=0", nItems, nKept)
	}
	got, _ := a.Store.Stories.Get(ctx, it.ID)
	if got.Status != workitem.StatusBacklog {
		t.Errorf("force did not overwrite: status=%q", got.Status)
	}
}

func TestMaterializeAppliesNewerHostedRow(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	old := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	fresh := old.Add(3 * time.Hour)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "T", Status: workitem.StatusBacklog,
	}, old)
	if err != nil {
		t.Fatal(err)
	}
	nItems, _, nKept, err := materializeWorkstate(ctx, a, map[string]bool{"stories": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, workitem.StatusDone, fresh)}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if nItems != 1 || nKept != 0 {
		t.Fatalf("applied=%d kept=%d, want applied=1 kept=0", nItems, nKept)
	}
	got, _ := a.Store.Stories.Get(ctx, it.ID)
	if got.Status != workitem.StatusDone {
		t.Errorf("newer hosted row not applied: status=%q", got.Status)
	}
}

func TestMaterializeLedgerAwareSkipWhenUpdatedAtRewound(t *testing.T) {
	// The vire shape: row.updated_at is already the old hosted value, but a
	// later local status_transition says done.
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 8, 13, 2, 16, 53, 0, time.UTC)
	transAt := time.Date(2026, 8, 13, 4, 26, 10, 0, time.UTC)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "T", Status: workitem.StatusBacklog,
	}, created)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"from": "postvalidation", "to": "done"})
	if _, err := a.Store.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindStatusTransition,
		Body: "postvalidation → done", Payload: payload,
	}, transAt); err != nil {
		t.Fatal(err)
	}

	nItems, _, nKept, err := materializeWorkstate(ctx, a, map[string]bool{"stories": true, "ledger": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, workitem.StatusBacklog, created)}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if nItems != 0 || nKept != 1 {
		t.Fatalf("applied=%d kept=%d, want applied=0 kept=1 (ledger-aware)", nItems, nKept)
	}
	got, _ := a.Store.Stories.Get(ctx, it.ID)
	if got.Status != workitem.StatusBacklog {
		// row was already backlog — the point is we did not need to change it,
		// and we must not have lost the later ledger event.
		t.Errorf("status = %q", got.Status)
	}
}
