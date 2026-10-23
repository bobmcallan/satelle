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
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindStatusTransition, Actor: "executor",
		Body: "backlog → done", Payload: payload,
	}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	drifts, err := verb.DetectDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 || drifts[0].ID != it.ID || drifts[0].Field != "status" ||
		drifts[0].Row != "backlog" || drifts[0].Chain != "done" {
		t.Fatalf("detect = %+v", drifts)
	}

	repaired, err := verb.RepairDrift(ctx, db.Stories, db.Ledger, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || repaired[0].Field != "status" {
		t.Fatalf("repaired %+v, want the one status field", repaired)
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

	again, err := verb.DetectDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second detect = %+v, want clean", again)
	}
}

func TestDetectDriftSkipsMatchingRow(t *testing.T) {
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
	drifts, err := verb.DetectDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Fatalf("matching row reported as drift: %+v", drifts)
	}
}

// sty_d84be8bc AC5: reconcile compares every folded field, not just status, and
// repairs each drifted field from the chain, naming it; a task, whose file is its
// source of truth, is never folded or touched.
func TestReconcileRepairsEveryDriftedFieldAndLeavesTasksAlone(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "folded", "body": "the body", "acceptance_criteria": "1. do it", "category": "feature", "tags": []string{"surface:cli"},
	}), &it); err != nil {
		t.Fatal(err)
	}
	task, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindTask, Title: "a task", Body: "file body"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the story row's body and tags directly, and the task's body.
	if _, err := db.SQL().Exec(`UPDATE work_items SET body = 'corrupt', tags = '["corrupt"]' WHERE id = ?`, it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE work_items SET body = 'edited on disk' WHERE id = ?`, task.ID); err != nil {
		t.Fatal(err)
	}

	drifts, err := verb.DetectDrift(ctx, db.Stories, db.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]verb.StoryDrift{}
	for _, d := range drifts {
		if d.ID == task.ID {
			t.Fatalf("a task was folded: %+v", d)
		}
		fields[d.Field] = d
	}
	if len(drifts) != 2 || fields["body"].Chain != "the body" || fields["body"].Row != "corrupt" || fields["tags"].Row != "corrupt" {
		t.Fatalf("drift = %+v, want the body and tags of the story, each with the chain's value", drifts)
	}

	repaired, err := verb.RepairDrift(ctx, db.Stories, db.Ledger, time.Now())
	if err != nil || len(repaired) != 2 {
		t.Fatalf("repaired = %+v, %v; want both fields named", repaired, err)
	}
	row, _ := db.Stories.Get(ctx, it.ID)
	if row.Body != "the body" || len(row.Tags) != len(it.Tags) {
		t.Errorf("row after repair = body %q tags %v, want the chain's %q %v", row.Body, row.Tags, "the body", it.Tags)
	}
	if got, _ := db.Stories.Get(ctx, task.ID); got.Body != "edited on disk" {
		t.Errorf("the task row was rewritten to %q", got.Body)
	}
	if again, _ := verb.DetectDrift(ctx, db.Stories, db.Ledger); len(again) != 0 {
		t.Errorf("drift after repair: %+v", again)
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
