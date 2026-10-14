package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// The hold and conflict records a work-state pass leaves for the session-start
// advisory and `story get|list` (sty_6e5fc41c). These run the real verbs against
// the fake hosted server and the fake hold surface.

func snapshotIn(t *testing.T, fx *holdFixture) string {
	t.Helper()
	out, err := runRoot(t, "sync", "workstate", "snapshot", "--server", fx.ts.URL)
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	return out
}

func (fx *holdFixture) recordedHolds(t *testing.T) map[string]hosted.HoldRecord {
	t.Helper()
	reg, err := hosted.LoadHolds(fx.ts.URL, "probe", fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func (fx *holdFixture) release(id string) {
	fx.hs.mu.Lock()
	defer fx.hs.mu.Unlock()
	delete(fx.hs.holders, id)
}

func (fx *holdFixture) conflicts(t *testing.T) map[string]syncstate.StoryConflict {
	t.Helper()
	st, _, err := syncstate.Load(config.GlobalDir(), fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	return st.StoryConflicts
}

// AC1: a snapshot records the story another location holds with holder, label,
// last-seen and the time it was checked.
func TestSnapshotRecordsAStoryAnotherLocationHolds(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	held := createStoryID(t, "held on another machine")
	mine := createStoryID(t, "held here")
	fx.push(t)
	fx.hs.setHolder(held, otherLoc, "desk")
	fx.hs.setHolder(mine, fx.loc, "box")

	before := time.Now().UTC().Add(-time.Second)
	out := snapshotIn(t, fx)

	if !strings.Contains(out, "full pass") {
		t.Fatalf("precondition: the first pass is full:\n%s", out)
	}
	reg := fx.recordedHolds(t)
	rec, ok := reg[held]
	if !ok {
		t.Fatalf("the held story is not recorded: %+v", reg)
	}
	if rec.Location != otherLoc || rec.Label != "desk" || rec.LastSeen != otherSeen {
		t.Errorf("record = %+v, want holder %s desk last seen %s", rec, otherLoc, otherSeen)
	}
	if rec.CheckedAt.Before(before) {
		t.Errorf("checked_at = %v, want a time of this pass", rec.CheckedAt)
	}
	if got := reg[mine].Location; got != fx.loc {
		t.Errorf("the hold held here is recorded as %q, want this location", got)
	}
}

// AC1: a hold released without any row changing is still seen. The second pass
// is a since pass in which the server does not redeliver the story, so only the
// recorded-hold set reaches it.
func TestSinceSnapshotClearsAReleasedHold(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	held := createStoryID(t, "released later")
	fx.push(t)
	fx.hs.setHolder(held, otherLoc, "desk")
	snapshotIn(t, fx)
	if _, ok := fx.recordedHolds(t)[held]; !ok {
		t.Fatal("precondition: the hold is recorded")
	}

	fx.release(held)
	out := snapshotIn(t, fx)

	if !strings.Contains(out, "since pass") {
		t.Fatalf("precondition: the second pass is a since pass:\n%s", out)
	}
	if last := fx.f.snapshotRequests(); last[len(last)-1].GetSinceToken() == "" {
		t.Fatal("precondition: the second pass sent a since token")
	}
	if _, ok := fx.recordedHolds(t)[held]; ok {
		t.Errorf("a released hold is still recorded: %+v", fx.recordedHolds(t))
	}
}

// AC1: an id already recorded as held is asked again even when the store holds no
// such story, so a release of a story this checkout does not carry is still seen.
func TestSinceSnapshotRechecksARecordedHoldWithNoLocalStory(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	createStoryID(t, "anchor")
	fx.push(t)
	snapshotIn(t, fx)
	const gone = "sty_notlocal"
	fx.hs.setHolder(gone, otherLoc, "desk")
	if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, gone, hosted.HoldRecord{Location: otherLoc, Label: "desk", LastSeen: otherSeen}); err != nil {
		t.Fatal(err)
	}

	snapshotIn(t, fx)
	if rec := fx.recordedHolds(t)[gone]; rec.CheckedAt.IsZero() {
		t.Fatalf("the recorded hold was not asked again: %+v", rec)
	}
	fx.release(gone)
	snapshotIn(t, fx)

	if _, ok := fx.recordedHolds(t)[gone]; ok {
		t.Errorf("a released hold on a story not in the store is still recorded: %+v", fx.recordedHolds(t))
	}
}

// AC1: a hold taken after the first pass, on a story the since pass does not
// redeliver, is recorded: every local story is a candidate.
func TestSinceSnapshotRecordsAHoldTakenWithoutARowChange(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "taken later")
	fx.push(t)
	snapshotIn(t, fx)

	fx.hs.setHolder(id, otherLoc, "desk")
	snapshotIn(t, fx)

	if rec := fx.recordedHolds(t)[id]; rec.Location != otherLoc {
		t.Fatalf("a hold taken between passes is not recorded: %+v", fx.recordedHolds(t))
	}
}

// AC1: a story that reached a terminal state is cleared whatever the server
// says of its hold.
func TestSnapshotClearsTheHoldOfAStoryThatFinished(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "finishes")
	fx.push(t)
	if err := hosted.RecordHold(fx.ts.URL, "probe", fx.repo, id, hosted.HoldRecord{Location: otherLoc, Label: "desk", LastSeen: otherSeen}); err != nil {
		t.Fatal(err)
	}
	fx.hs.setHolder(id, otherLoc, "desk")
	_, terminal := executorStateFor(t, id)
	putInState(t, id, terminal, "", "", false)

	snapshotIn(t, fx)

	if _, ok := fx.recordedHolds(t)[id]; ok {
		t.Errorf("a finished story keeps its recorded hold: %+v", fx.recordedHolds(t))
	}
}

// AC1: a pull records the same entry.
func TestPullRecordsTheSameHoldEntry(t *testing.T) {
	fx := newHoldFixture(t, pullSyncTm)
	seedHostedHoldStory(fx.f, "sty_flight", "backlog")
	fx.hs.setHolder("sty_flight", otherLoc, "desk")

	pullIn(t, fx)

	rec, ok := fx.recordedHolds(t)["sty_flight"]
	if !ok || rec.Location != otherLoc || rec.Label != "desk" || rec.LastSeen != otherSeen || rec.CheckedAt.IsZero() {
		t.Fatalf("pull recorded %+v, want holder %s desk last seen %s with a check time", rec, otherLoc, otherSeen)
	}
}

// A hold lookup that fails must not fail the pass, and must not clear a record
// it could not verify.
func TestSnapshotKeepsTheRecordWhenTheHoldSurfaceIsDown(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "outage")
	fx.push(t)
	fx.hs.setHolder(id, otherLoc, "desk")
	snapshotIn(t, fx)

	fx.hs.setDown(true)
	snapshotIn(t, fx)

	if fx.recordedHolds(t)[id].Location != otherLoc {
		t.Fatalf("an outage cleared the record: %+v", fx.recordedHolds(t))
	}
}

// AC2: a local status ahead of hosted is recorded as a conflict and the local
// copy is left as it was; pushing the local copy makes both identical and the
// next pass clears the record.
func TestSnapshotRecordsALocalAheadConflictAndClearsItOnceIdentical(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "diverges")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)

	snapshotIn(t, fx)

	c, ok := fx.conflicts(t)[id]
	if !ok || c.Reason != conflictLocalAhead {
		t.Fatalf("conflicts = %+v, want %s recorded as %q", fx.conflicts(t), id, conflictLocalAhead)
	}
	if got := fx.status(t, id); got != exec {
		t.Fatalf("recording the conflict changed the local status to %q, want %q", got, exec)
	}

	fx.push(t)
	snapshotIn(t, fx)

	if _, ok := fx.conflicts(t)[id]; ok {
		t.Errorf("a conflict is still recorded once both copies are identical: %+v", fx.conflicts(t))
	}
	if got := fx.status(t, id); got != exec {
		t.Errorf("local status = %q after the clearing pass, want %q", got, exec)
	}
}

// AC2: a pull refused with the work-state pull conflict records it too.
func TestPullRefusedForADifferingStoryRecordsAConflict(t *testing.T) {
	fx := newHoldFixture(t, holdNoGit)
	id := createStoryID(t, "refused")
	fx.push(t)
	exec, _ := executorStateFor(t, id)
	time.Sleep(1100 * time.Millisecond) // time-subject: the local row must be a second newer than the hosted one
	putInState(t, id, exec, "", "", false)

	out, err := runRoot(t, "sync", "workstate", "pull", "--server", fx.ts.URL)
	if err == nil || !strings.Contains(err.Error(), "pull conflict") {
		t.Fatalf("pull = %v\n%s, want the work-state pull conflict", err, out)
	}

	if c := fx.conflicts(t)[id]; c.Reason != conflictPullRefused {
		t.Fatalf("conflicts = %+v, want %s recorded as %q", fx.conflicts(t), id, conflictPullRefused)
	}
	if got := fx.status(t, id); got != exec {
		t.Errorf("a refused pull changed the local status to %q", got)
	}
}
