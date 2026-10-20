package cli

// Overlap is recorded, not arbitrated (sty_9914df10 AC5): a sync pass that finds
// a story's copies disagreeing, or a push a hold refuses, writes one `overlap`
// row onto the story's ledger, and the row travels on the next push. The refusal
// and the exit code are the ones the existing tests already pin.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

type overlapRow struct {
	LocalHolder  string `json:"local_holder"`
	RemoteHolder string `json:"remote_holder"`
	RemoteUser   string `json:"remote_user"`
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

// pushedOverlaps counts the overlap rows the fake server has received for a story.
func pushedOverlaps(fx *holdFixture, id string) int {
	fx.f.mu.Lock()
	defer fx.f.mu.Unlock()
	n := 0
	for _, batch := range fx.f.posts["probe"] {
		for _, e := range batch["ledger"].([]any) {
			row := e.(map[string]any)
			if row["kind"] == ledger.KindOverlap && row["story_id"] == id {
				n++
			}
		}
	}
	return n
}

func TestPushRefusedByAHoldRecordsAnOverlapRowThatTravels(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "held by someone else")
	fx.hs.setHolder(id, otherLoc, "desk")
	fx.hs.mu.Lock()
	h := fx.hs.holders[id]
	h.UserID = "usr_someone_else"
	fx.hs.holders[id] = h
	fx.hs.mu.Unlock()

	out := fx.push(t) // exit zero, as before
	if !strings.Contains(out, "skip "+id+" (held elsewhere)") {
		t.Fatalf("the refusal output changed:\n%s", out)
	}
	if fx.f.hasItem("probe", id) {
		t.Error("the held story's record must still not be pushed")
	}
	rows := overlapRows(t, id)
	if len(rows) != 1 {
		t.Fatalf("overlap rows = %+v, want exactly one", rows)
	}
	r := rows[0]
	if r.Source != "hold" || r.RemoteHolder != "desk" || r.RemoteUser != "usr_someone_else" || r.LocalHolder != fx.loc || r.Reason == "" {
		t.Errorf("row = %+v, want source hold naming local %s and remote desk / usr_someone_else", r, fx.loc)
	}

	// The row is read into the very push that found the overlap, so it is already
	// on the server; the same overlap seen again is neither recorded nor sent again.
	if n := pushedOverlaps(fx, id); n != 1 {
		t.Errorf("overlap rows in the Apply ledger = %d, want 1 (the row must travel)", n)
	}
	fx.push(t)
	if n := len(overlapRows(t, id)); n != 1 {
		t.Errorf("the same overlap recorded %d rows", n)
	}
	if n := pushedOverlaps(fx, id); n != 1 {
		t.Errorf("a second push re-sent the overlap row: %d in the Apply ledger", n)
	}
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
		t.Errorf("row = %+v, want source conflict, remote unknown (no hold), reason %q", r, conflictLocalAhead)
	}

	snapshotIn(t, fx) // the conflict is still there: no second row
	if n := len(overlapRows(t, id)); n != 1 {
		t.Errorf("a repeated pass recorded %d rows", n)
	}
	fx.push(t)
	if n := pushedOverlaps(fx, id); n != 1 {
		t.Errorf("overlap rows in the Apply ledger = %d, want 1", n)
	}
}

func TestPullConflictNamesTheHolderWhenTheServerKnowsOne(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "refused with a holder")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)
	fx.hs.setHolder(id, otherLoc, "desk")

	if _, err := runRoot(t, "sync", "workstate", "pull", "--server", fx.ts.URL); err == nil {
		t.Fatal("precondition: the pull is refused for the differing story")
	}
	rows := overlapRows(t, id)
	if len(rows) != 1 || rows[0].Source != "conflict" || rows[0].RemoteHolder != "desk" {
		t.Errorf("rows = %+v, want one conflict row naming the hosted holder desk", rows)
	}
}
