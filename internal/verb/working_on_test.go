package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func workingOnRows(t *testing.T, db *store.DB, id string) []verb.WorkingOnPayload {
	t.Helper()
	entries, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindWorkingOn)
	if err != nil {
		t.Fatal(err)
	}
	var out []verb.WorkingOnPayload
	for _, e := range entries {
		var p verb.WorkingOnPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("working_on payload %s: %v", e.Payload, err)
		}
		out = append(out, p)
	}
	return out
}

// Engaging a story appends one working_on row naming the location, session and
// state (sty_40853d02 AC4, sty_9c782f0e AC3). Each new performing state appends
// another; a same-status re-set from the same place appends none; a second story
// engaging alongside writes its own row and is not refused.
func TestEngagingAppendsWorkingOnRows(t *testing.T) {
	db := wireWithWorkflowsStore(t, singleStoryWF)
	verb.SetLocationResolver(func() (string, string) { return "loc_unit", "unit box" })
	t.Setenv(config.SessionEnv, "sess_unit")

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Second", "category": "feature"}), &b)
	if rows := workingOnRows(t, db, a.ID); len(rows) != 0 {
		t.Fatalf("a story nobody engaged has working_on rows: %+v", rows)
	}

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	rows := workingOnRows(t, db, a.ID)
	if len(rows) != 1 {
		t.Fatalf("engaging wrote %d working_on rows, want 1: %+v", len(rows), rows)
	}
	if r := rows[0]; r.Location != "loc_unit" || r.Label != "unit box" || r.Session != "sess_unit" || r.State != "plan" {
		t.Errorf("row = %+v, want loc_unit / unit box / sess_unit / plan", r)
	}

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if rows := workingOnRows(t, db, a.ID); len(rows) != 2 || rows[1].State != "in_progress" {
		t.Fatalf("the next performing state: rows = %+v, want a second row at in_progress", rows)
	}
	call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"})
	if rows := workingOnRows(t, db, a.ID); len(rows) != 2 {
		t.Fatalf("a same-status re-set from the same place wrote a row: %+v", rows)
	}

	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}); err != nil {
		t.Fatalf("a second story must engage alongside the first: %v", err)
	}
	if rows := workingOnRows(t, db, b.ID); len(rows) != 1 {
		t.Fatalf("the second story's engagement: rows = %+v, want one", rows)
	}
}

// TestWorkingOnNeverRefuses (sty_9c782f0e AC3): presence is advisory. A story
// whose newest working_on row names another location and session is still
// engaged, moved and closed from here, and this location's row is added beside
// it rather than replacing it.
func TestWorkingOnNeverRefuses(t *testing.T) {
	db := wireWithWorkflowsStore(t, singleStoryWF)
	verb.SetLocationResolver(func() (string, string) { return "loc_here", "here" })
	t.Setenv(config.SessionEnv, "sess_here")

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Shared", "category": "feature"}), &a)
	other, _ := json.Marshal(verb.WorkingOnPayload{Location: "loc_there", Label: "there", Session: "sess_there", Worktree: "/elsewhere", State: "plan"})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: a.ID, Kind: ledger.KindWorkingOn, Actor: "executor", Body: "working_on there", Payload: other,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	for _, st := range []string{"plan", "in_progress", "done"} {
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": st}); err != nil {
			t.Fatalf("%s → %s with another location's presence: %v", a.ID, st, err)
		}
	}
	rows := workingOnRows(t, db, a.ID)
	if len(rows) != 3 || rows[0].Location != "loc_there" || rows[1].Location != "loc_here" || rows[2].State != "in_progress" {
		t.Fatalf("rows = %+v, want the other location's row kept and this one's added per performing state", rows)
	}
}

// With no location wired (an unbound repo) the row still records the engagement,
// naming only the session and state.
func TestWorkingOnRowWithoutALocation(t *testing.T) {
	db := wireWithWorkflowsStore(t, singleStoryWF)
	verb.ClearLocationResolver()
	t.Setenv(config.SessionEnv, "sess_solo")

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Solo", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	rows := workingOnRows(t, db, a.ID)
	if len(rows) != 1 || rows[0].Location != "" || rows[0].Session != "sess_solo" {
		t.Fatalf("rows = %+v, want one row with no location and session sess_solo", rows)
	}
}
