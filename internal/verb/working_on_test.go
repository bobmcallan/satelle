package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// Taking a fresh engagement seat appends one working_on row naming the location,
// session and state (sty_40853d02 AC4). A step that moves on while the seat is held
// appends none, a refused engage appends none, and a seat taken again after it was
// released appends another.
func TestFreshSeatAppendsOneWorkingOnRow(t *testing.T) {
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

	// The next step while the seat is held re-seats nothing.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if rows := workingOnRows(t, db, a.ID); len(rows) != 1 {
		t.Fatalf("a step under a held seat wrote a row: %+v", rows)
	}

	// A refused engage leaves no row: the seat was never taken.
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}); err == nil || !strings.Contains(err.Error(), "engagement seat") {
		t.Fatalf("second engage should be refused on the seat, got %v", err)
	}
	if rows := workingOnRows(t, db, b.ID); len(rows) != 0 {
		t.Fatalf("a refused engage wrote working_on rows: %+v", rows)
	}

	// A seat taken again after the first was released is a fresh seat.
	if err := db.Leases.ForceRelease(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"})
	rows = workingOnRows(t, db, a.ID)
	if len(rows) != 2 || rows[1].State != "in_progress" {
		t.Fatalf("re-engaging after a release: rows = %+v, want a second row at in_progress", rows)
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
