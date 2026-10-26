package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
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

// materializeWorkstate applies one batch the way a single-page pass does: upsert
// the rows (which rebuilds each applied story from its chain), regenerate the
// story views.
func materializeWorkstate(ctx context.Context, a *app.App, optIn map[string]bool, items []hosted.WorkstateItem, ledgerRows []hosted.WorkstateLedgerRow, force bool) (nItems, nLedger, nKept int, err error) {
	now := time.Now().UTC()
	_, nItems, nLedger, nKept, err = upsertWorkstatePage(ctx, a, optIn, items, ledgerRows, force, now, nil, nil)
	if err != nil {
		return nItems, nLedger, nKept, err
	}
	if _, _, verr := verb.SyncStoryBacklog(ctx, a.Store.Stories, now); verr != nil {
		return nItems, nLedger, nKept, verr
	}
	return nItems, nLedger, nKept, nil
}

func hostedStory(id, status string, updated time.Time) hosted.WorkstateItem {
	rec, _ := json.Marshal(workstateItemWire{
		ID: id, Kind: string(workitem.KindStory), Status: status,
		Title: "T", UpdatedAt: updated, CreatedAt: updated.Add(-time.Hour),
	})
	return hosted.WorkstateItem{ID: id, Kind: string(workitem.KindStory), Status: status, Title: "T", Record: rec}
}

func TestWorkstateItemRoundTripPreservesParkOrigin(t *testing.T) {
	it := workitem.Item{
		ID: "sty_park", Kind: workitem.KindStory, Title: "T",
		Status: "blocked", ParkOrigin: "in_progress",
		UpdatedAt: time.Date(2026, 8, 16, 23, 19, 50, 0, time.UTC),
		CreatedAt: time.Date(2026, 8, 16, 22, 0, 0, 0, time.UTC),
	}
	raw, err := marshalWorkstateItem(it)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseWorkstateItem(hosted.WorkstateItem{
		ID: it.ID, Kind: string(it.Kind), Status: it.Status, Title: it.Title, Record: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ParkOrigin != "in_progress" {
		t.Errorf("round-trip park_origin = %q", got.ParkOrigin)
	}
}

// A task's declared inputs and an execution's values ride the workstate wire
// (inputs_decl / inputs), and the values come back typed.
func TestWorkstateItemRoundTripKeepsInputs(t *testing.T) {
	decls, _ := workitem.ParseInputDecls("story_id:string:required, retries:int")
	for _, it := range []workitem.Item{
		{ID: "tsk_in", Kind: workitem.KindTask, Title: "T", Status: "active", Inputs: decls},
		{ID: "exe_in", Kind: workitem.KindExecution, Title: "T", Status: "backlog",
			InputValues: map[string]any{"story_id": "sty_1", "retries": int64(3), "on": true}},
	} {
		raw, err := marshalWorkstateItem(it)
		if err != nil {
			t.Fatal(err)
		}
		got, err := parseWorkstateItem(hosted.WorkstateItem{ID: it.ID, Kind: string(it.Kind), Record: raw})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Inputs, it.Inputs) || !reflect.DeepEqual(got.InputValues, it.InputValues) {
			t.Errorf("%s: got decls %+v values %#v", it.ID, got.Inputs, got.InputValues)
		}
	}
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

func TestMaterializeLaterLocalTransitionWinsWhenUpdatedAtRewound(t *testing.T) {
	// The vire shape: row.updated_at is already the old hosted value, but a
	// later local status_transition says done. The chain is the authority on
	// status (sty_d84be8bc): the hosted row is applied, and the row it leaves is
	// rebuilt from the fold, so the later transition is not lost.
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

	if _, _, _, err := materializeWorkstate(ctx, a, map[string]bool{"stories": true, "ledger": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, workitem.StatusBacklog, created)}, nil, false); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Store.Stories.Get(ctx, it.ID)
	if got.Status != workitem.StatusDone {
		t.Errorf("status = %q, want done: the later local transition is the chain's last word", got.Status)
	}
	if drifts, derr := verb.DetectDrift(ctx, a.Store.Stories, a.Store.Ledger); derr != nil || len(drifts) != 0 {
		t.Errorf("drift after the pull = %+v, %v", drifts, derr)
	}
}

// sty_38915987 AC2: local plan + hosted in_progress with a matching ledger TO
// must land in_progress after materialize (Upsert preserves status; SetStatus
// applies when ledger TO equals incoming). DetectStatusDrift stays empty.
func TestMaterializeAppliesHostedForwardWithLocalHistory(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 1, 5, 40, 0, 0, time.UTC)
	hostedAt := created.Add(time.Hour)
	it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "T", Status: "plan",
	}, created)
	if err != nil {
		t.Fatal(err)
	}
	// Local row is plan; hosted carries in_progress plus the transition ledger.
	ledID := "evt_hosted_fwd"
	ledRec, _ := json.Marshal(map[string]any{
		"id": ledID, "story_id": it.ID, "kind": ledger.KindStatusTransition,
		"body":       "plan → in_progress",
		"payload":    map[string]string{"from": "plan", "to": "in_progress"},
		"created_at": hostedAt,
	})
	ledRow := hosted.WorkstateLedgerRow{
		ID: ledID, StoryID: it.ID, Kind: ledger.KindStatusTransition,
		Record: ledRec,
	}

	nItems, nLedger, nKept, err := materializeWorkstate(ctx, a,
		map[string]bool{"stories": true, "ledger": true},
		[]hosted.WorkstateItem{hostedStory(it.ID, "in_progress", hostedAt)},
		[]hosted.WorkstateLedgerRow{ledRow}, false)
	if err != nil {
		t.Fatal(err)
	}
	if nItems != 1 || nKept != 0 {
		t.Fatalf("applied=%d kept=%d, want applied=1 kept=0", nItems, nKept)
	}
	if nLedger != 1 {
		t.Fatalf("ledger applied=%d, want 1", nLedger)
	}
	got, gerr := a.Store.Stories.Get(ctx, it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
	drifts, derr := verb.DetectDrift(ctx, a.Store.Stories, a.Store.Ledger)
	if derr != nil {
		t.Fatal(derr)
	}
	if len(drifts) != 0 {
		t.Fatalf("DetectStatusDrift = %+v, want clean", drifts)
	}
}
