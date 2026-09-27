package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// appendVerdict writes a review_accept/review_reject row for storyID under
// skill/agent/model, the shape costview.GateValue joins to invocations.
func appendVerdict(t *testing.T, db *store.DB, storyID, skill string, accept bool, at time.Time) {
	t.Helper()
	kind := ledger.KindReviewReject
	if accept {
		kind = ledger.KindReviewAccept
	}
	raw, _ := json.Marshal(map[string]any{"skill": skill, "agent": "reviewer", "model": "opus", "accept": accept})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: storyID, Kind: kind, Actor: "reviewer", Payload: raw,
	}, at); err != nil {
		t.Fatal(err)
	}
}

// gateValueFixture builds a parent with one story child (and a grandchild via
// the child), plus an unrelated story, each carrying one gate invocation with
// a known cost and a reject verdict, so scope tests can assert exactly which
// stories' rows fed the report. Costs: parent 1, child 2, grandchild 4,
// unrelated 8 — a total identifies the included set unambiguously.
func gateValueFixture(t *testing.T, db *store.DB, at time.Time) (parent, child, grand, other workitem.Item) {
	t.Helper()
	ctx := context.Background()
	mk := func(title, parentID string) workitem.Item {
		it, err := db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: title, Status: "backlog", ParentID: parentID,
		}, at)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	parent = mk("epic", "")
	child = mk("child", parent.ID)
	grand = mk("grandchild", child.ID)
	other = mk("unrelated", "")
	for id, cost := range map[string]float64{parent.ID: 1, child.ID: 2, grand.ID: 4, other.ID: 8} {
		appendInvocation(t, db, id, map[string]any{
			"from": "in_progress", "to": "integration", "agent": "reviewer", "model": "opus",
			"skill": "gate-a", "usage_available": true, "cost_usd": cost,
			"tokens_in_fresh": 10, "tokens_out": 5,
		}, at)
		appendVerdict(t, db, id, "gate-a", false, at)
	}
	return parent, child, grand, other
}

func gateRow(t *testing.T, rep costview.GateValueReport, skill string) costview.GateValueRow {
	t.Helper()
	for _, r := range rep.Rows {
		if r.Skill == skill && r.Seat == "reviewer@opus" {
			return r
		}
	}
	t.Fatalf("no %s/reviewer@opus row in %+v", skill, rep.Rows)
	return costview.GateValueRow{}
}

// TestComputeGateValueStoryScope pins that StoryID reads only that story's
// ledger (and wins over EpicID when both are set).
func TestComputeGateValueStoryScope(t *testing.T) {
	db := wireActualWF(t)
	_, child, _, _ := gateValueFixture(t, db, time.Now())

	rep, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{StoryID: child.ID, EpicID: "sty_ignored"})
	if err != nil {
		t.Fatal(err)
	}
	r := gateRow(t, rep, "gate-a")
	if r.Invocations != 1 || r.CostUSD != 2 || r.Rejects != 1 || r.Accepts != 0 {
		t.Errorf("story scope row = %+v, want exactly the child's invocation ($2), 1 reject", r)
	}
}

// TestComputeGateValueEpicScope pins that EpicID covers the root and every
// story descendant (any depth) and excludes an unrelated story.
func TestComputeGateValueEpicScope(t *testing.T) {
	db := wireActualWF(t)
	parent, _, _, _ := gateValueFixture(t, db, time.Now())

	rep, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{EpicID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := gateRow(t, rep, "gate-a")
	if r.Invocations != 3 || r.CostUSD != 7 || r.Rejects != 3 {
		t.Errorf("epic scope row = %+v, want parent+child+grandchild ($7, 3 invocations, 3 rejects), not the unrelated story", r)
	}
	if r.CostPerRejectUSD == nil || *r.CostPerRejectUSD < 2.33 || *r.CostPerRejectUSD > 2.34 {
		t.Errorf("cost per reject = %v, want 7/3", r.CostPerRejectUSD)
	}

	if _, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{EpicID: "sty_missing"}); err == nil {
		t.Error("an unknown epic id must surface an error, not an empty report")
	}
}

// TestComputeGateValueRepoWideScan pins the repo-wide branch: every story's
// invocation and verdict rows via ForEachKind, the date range bounding it,
// and no non-gate ledger kinds leaking in.
func TestComputeGateValueRepoWideScan(t *testing.T) {
	db := wireActualWF(t)
	_, _, _, other := gateValueFixture(t, db, time.Now().Add(-48*time.Hour))
	recent := time.Now()
	appendInvocation(t, db, other.ID, map[string]any{
		"from": "a", "to": "b", "agent": "reviewer", "model": "opus", "skill": "gate-a",
		"usage_available": true, "cost_usd": 16.0,
	}, recent)
	appendTransition(t, db, other.ID, "backlog", "in_progress", recent)

	all, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := gateRow(t, all, "gate-a")
	if r.Invocations != 5 || r.CostUSD != 31 || r.Rejects != 4 {
		t.Errorf("repo-wide row = %+v, want 5 invocations, $31 (1+2+4+8+16), 4 rejects", r)
	}

	windowed, err := verb.ComputeGateValue(context.Background(), verb.GateValueOptions{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	r = gateRow(t, windowed, "gate-a")
	if r.Invocations != 1 || r.CostUSD != 16 || r.Rejects != 0 {
		t.Errorf("windowed row = %+v, want only the recent $16 invocation", r)
	}
}
