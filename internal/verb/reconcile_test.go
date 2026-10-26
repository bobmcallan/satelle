package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
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

// sty_6f952dd0 AC4: a story whose chain holds a terminal transition followed by a
// stale non-terminal one, with the seeded definition_snapshot (status done) between
// them as on sty_41f61669, is reported — its row, which a pull rebuilt from the wrong
// fold, shows as drift, and the stale event is named — and the repair brings the row
// back to done while the append-only stale event stays listed.
func TestReconcileReportsATerminalTransitionFollowedByAStaleOne(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "late", Status: workitem.StatusBacklog}, now)
	if err != nil {
		t.Fatal(err)
	}
	// The sty_41f61669 shape: the done transition, the seeded definition_snapshot
	// (status done) pulled in after it, then an earlier killed attempt's events,
	// among them its plan → in_progress, linked after the snapshot.
	snap := it
	snap.Status = workitem.StatusDone
	seq := 0
	appendEv := func(kind, body string, payload json.RawMessage) ledger.Entry {
		seq++
		e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: it.ID, Kind: kind, Body: body, Payload: payload}, now.Add(time.Duration(seq)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	transition := func(from, to string) ledger.Entry {
		payload, _ := json.Marshal(map[string]string{"from": from, "to": to})
		return appendEv(ledger.KindStatusTransition, from+" → "+to, payload)
	}
	note := json.RawMessage(`{"note":"earlier attempt"}`)
	transition("backlog", "in_progress")
	transition("in_progress", "done")
	appendEv(ledger.KindDefinitionSnapshot, "definition snapshot", storyfold.SnapshotPayloadOf(snap, true))
	appendEv(ledger.KindWorkingOn, "earlier attempt", note)
	appendEv(ledger.KindStepSummary, "earlier attempt", note)
	stale := transition("plan", "in_progress")
	appendEv(ledger.KindStepSummary, "earlier attempt", note)
	// The row sty_41f61669 showed: what a rebuild from the stale-last fold wrote.
	if _, err := db.SQL().Exec(`UPDATE work_items SET status = 'in_progress' WHERE id = ?`, it.ID); err != nil {
		t.Fatal(err)
	}

	drifts, err := verb.DetectDrift(ctx, db.Stories, db.Ledger)
	if err != nil || len(drifts) != 1 || drifts[0].Field != "status" || drifts[0].Row != "in_progress" || drifts[0].Chain != "done" {
		t.Fatalf("drift = %+v, %v; want status in_progress → done", drifts, err)
	}
	reported, err := verb.DetectStaleTransitions(ctx, db.Stories, db.Ledger)
	if err != nil || len(reported) != 1 || reported[0].EventID != stale.ID || reported[0].StoryID != it.ID {
		t.Fatalf("stale = %+v, %v; want the late event %s", reported, err, stale.ID)
	}

	if _, err := verb.RepairDrift(ctx, db.Stories, db.Ledger, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Stories.Get(ctx, it.ID); got.Status != workitem.StatusDone {
		t.Errorf("repaired status = %q, want done", got.Status)
	}
	if again, _ := verb.DetectDrift(ctx, db.Stories, db.Ledger); len(again) != 0 {
		t.Errorf("drift after repair: %+v", again)
	}
	if again, _ := verb.DetectStaleTransitions(ctx, db.Stories, db.Ledger); len(again) != 1 {
		t.Errorf("the stale event is a fact of the chain and stays listed: %+v", again)
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
