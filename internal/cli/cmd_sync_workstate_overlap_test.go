package cli

// Overlap is recorded, not arbitrated (sty_9914df10 AC5): a sync pass that finds
// a story's copies disagreeing writes one `overlap` row onto the story's ledger,
// and the row travels on the next push. The refusal and the exit code are the
// ones the existing tests already pin. No story is held (sty_40853d02): the other
// side is named from the latest working_on row another location wrote.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

type overlapRow struct {
	LocalHolder  string `json:"local_holder"`
	RemoteHolder string `json:"remote_holder"`
	Reason       string `json:"reason"`
	Source       string `json:"source"`
}

// overlapRows reads the overlap rows of a story from this repo's store.
func overlapRows(t *testing.T, id string) []overlapRow {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	entries, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindOverlap)
	if err != nil {
		t.Fatal(err)
	}
	var rows []overlapRow
	for _, e := range entries {
		var r overlapRow
		if err := json.Unmarshal(e.Payload, &r); err != nil {
			t.Fatalf("overlap payload %s: %v", e.Payload, err)
		}
		rows = append(rows, r)
	}
	return rows
}

func TestSnapshotConflictRecordsAnOverlapRowOnce(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "diverges")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)

	snapshotIn(t, fx)
	if _, ok := fx.conflicts(t)[id]; !ok {
		t.Fatalf("precondition: a conflict is recorded, got %+v", fx.conflicts(t))
	}
	rows := overlapRows(t, id)
	if len(rows) != 1 {
		t.Fatalf("overlap rows = %+v, want exactly one for the conflict", rows)
	}
	if r := rows[0]; r.Source != "conflict" || r.RemoteHolder != "unknown" || r.Reason != conflictLocalAhead || r.LocalHolder != fx.loc {
		t.Errorf("row = %+v, want source conflict, remote unknown (no other location worked it), reason %q", r, conflictLocalAhead)
	}

	snapshotIn(t, fx) // the conflict is still there: no second row
	if n := len(overlapRows(t, id)); n != 1 {
		t.Errorf("a repeated pass recorded %d rows", n)
	}
	fx.push(t)
	if n := fx.f.ledgerKindCount("probe", id, ledger.KindOverlap); n != 1 {
		t.Errorf("overlap rows on the server = %d, want 1", n)
	}
}

// workedElsewhere appends a working_on row from another location to the story, as
// a pull of that location's events would bring it.
func workedElsewhere(t *testing.T, id, loc, label string) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	payload, _ := json.Marshal(verb.WorkingOnPayload{Location: loc, Label: label, Session: "sess_other", State: "in_progress"})
	if _, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: id, Kind: ledger.KindWorkingOn, Actor: "executor", Body: "working on", Payload: payload,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestPullConflictNamesTheOtherLocationThatWorkedIt(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "refused, worked elsewhere")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)
	workedElsewhere(t, id, otherLoc, "desk")

	if _, err := runRoot(t, "sync", "workstate", "pull", "--server", fx.ts.URL); err == nil {
		t.Fatal("precondition: the pull is refused for the differing story")
	}
	rows := overlapRows(t, id)
	if len(rows) != 1 || rows[0].Source != "conflict" || rows[0].RemoteHolder != "desk" {
		t.Errorf("rows = %+v, want one conflict row naming desk, the location that last worked it", rows)
	}
}
