package cli

// Overlap is recorded, not arbitrated (sty_9914df10), and never on the chain
// (sty_199ad82b): a sync pass that finds a story's copies disagreeing records the
// overlap in the work-state sync state, naming both sides, and the story's chain
// gains no event. The refusal and the exit code are the ones the existing tests
// already pin. No story is held (sty_40853d02): the other side is named from the
// latest working_on row another location wrote.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// passChain is a story's local chain as the last sync pass left it. It opens the
// store read-only: store.Open seeds a story whose chain defines no title, which
// would add events no pass made. A checkout with no store yet holds no chain.
func passChain(t *testing.T, id string) []ledger.Entry {
	t.Helper()
	path := runtimeDBPath(t)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	db, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open db read-only: %v", err)
	}
	defer func() { _ = db.Close() }()
	chain, err := db.Ledger.StoryChain(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

// sameEvents fails when a pass changed a story's chain: the same events, by id,
// before and after.
func sameEvents(t *testing.T, what string, before, after []ledger.Entry) {
	t.Helper()
	b, a := idsOf(before), idsOf(after)
	slices.Sort(b)
	slices.Sort(a)
	if !slices.Equal(a, b) {
		t.Errorf("%s changed the chain: %d event(s) %v, want the %d before it %v", what, len(a), a, len(b), b)
	}
	for _, e := range after {
		if e.Kind == ledger.KindOverlap {
			t.Errorf("%s wrote an overlap event %s onto the chain", what, e.ID)
		}
	}
}

func TestSnapshotConflictRecordsTheOverlapInSyncStateNotOnTheChain(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "diverges")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)
	before := passChain(t, id)

	snapshotIn(t, fx)
	c, ok := fx.conflicts(t)[id]
	if !ok {
		t.Fatalf("precondition: a conflict is recorded, got %+v", fx.conflicts(t))
	}
	if c.Source != "conflict" || c.RemoteHolder != "unknown" || c.Reason != conflictLocalAhead || c.LocalHolder != fx.loc {
		t.Errorf("conflict = %+v, want source conflict, remote unknown (no other location worked it), reason %q, local %s", c, conflictLocalAhead, fx.loc)
	}
	sameEvents(t, "the snapshot", before, passChain(t, id))

	snapshotIn(t, fx) // the conflict is still there: still nothing on the chain
	sameEvents(t, "a repeated snapshot", before, passChain(t, id))
	if again := fx.conflicts(t)[id]; !again.At.Equal(c.At) {
		t.Errorf("a repeated pass moved the conflict's time from %v to %v", c.At, again.At)
	}
	fx.push(t)
	if n := fx.f.ledgerKindCount("probe", id, ledger.KindOverlap); n != 0 {
		t.Errorf("overlap rows on the server = %d, want none", n)
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
	before := passChain(t, id)

	if _, err := runRoot(t, "sync", "workstate", "pull", "--server", fx.ts.URL); err == nil {
		t.Fatal("precondition: the pull is refused for the differing story")
	}
	if c := fx.conflicts(t)[id]; c.Source != "conflict" || c.RemoteHolder != "desk" || c.LocalHolder != fx.loc {
		t.Errorf("conflict = %+v, want one naming desk, the location that last worked it", c)
	}
	sameEvents(t, "the refused pull", before, passChain(t, id))
}
