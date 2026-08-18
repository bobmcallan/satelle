package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestDetectAndRepairStatusDrift(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "drifted", Status: workitem.StatusBacklog,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"from": "backlog", "to": "done"})
	e, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindStatusTransition, Actor: "executor",
		Body: "backlog → done", Payload: payload,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	drifts, err := verb.DetectStatusDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 || drifts[0].ID != it.ID || drifts[0].LedgerStatus != "done" {
		t.Fatalf("detect = %+v", drifts)
	}
	if drifts[0].TransitionID != e.ID {
		t.Errorf("transition id = %q, want %s", drifts[0].TransitionID, e.ID)
	}

	repaired, err := verb.RepairStatusDrift(ctx, db.Stories, db.Ledger, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 {
		t.Fatalf("repaired %d, want 1", len(repaired))
	}
	got, err := db.Stories.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workitem.StatusDone {
		t.Errorf("repaired status = %q, want done", got.Status)
	}
	if !got.UpdatedAt.After(it.UpdatedAt) {
		t.Error("repair must bump updated_at so a later push sees the row")
	}
	recs, err := db.Ledger.ListByStory(ctx, it.ID, ledger.KindStatusReconcile)
	if err != nil || len(recs) != 1 {
		t.Fatalf("status_reconcile rows = %d err=%v", len(recs), err)
	}

	again, err := verb.DetectStatusDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second detect = %+v, want clean", again)
	}
}

func TestDetectStatusDriftSkipsMatchingRow(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "ok", Status: workitem.StatusDone,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"from": "backlog", "to": "done"})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindStatusTransition, Body: "backlog → done", Payload: payload,
	}, now); err != nil {
		t.Fatal(err)
	}
	drifts, err := verb.DetectStatusDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Fatalf("matching row reported as drift: %+v", drifts)
	}
}

func TestTransitionToPrefersPayload(t *testing.T) {
	e := ledger.Entry{
		Body:    "backlog → cancelled",
		Payload: json.RawMessage(`{"from":"backlog","to":"done"}`),
	}
	if got := verb.TransitionTo(e); got != "done" {
		t.Errorf("TransitionTo = %q, want done from payload", got)
	}
	if got := verb.TransitionTo(ledger.Entry{Body: "in_progress → release"}); got != "release" {
		t.Errorf("body fallback = %q, want release", got)
	}
}
