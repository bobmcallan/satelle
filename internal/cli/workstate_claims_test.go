package cli

import (
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// A drive-stopped row leaves the story in a performing step, where a push never
// releases a hold: the run that stopped it released its own (sty_aaafe4e2), and a
// hold taken on the story afterwards — a person resuming it — is theirs to keep.
func TestReleaseAtRestKeepsAHoldTakenAfterADriveStopped(t *testing.T) {
	hs := newFakeHoldServer()
	w := newHoldWorldWith(t, holdSyncToml, hs.routes)
	loc, err := hosted.LocationID(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	id := createStoryID(t, "stopped by a drive, then resumed")
	putInState(t, id, "in_progress", "", "", false)
	if out, err := runRoot(t, "ledger", "append", "--story", id, "--kind", ledger.KindDriveStopped, "--actor", "orchestrator",
		"--body", "step in_progress, performer executor: the executor run failed"); err != nil {
		t.Fatalf("append: %v\n%s", err, out)
	}
	// The hold is taken after the stop.
	hs.setHolder(id, loc, "box")
	if err := hosted.RecordHold(w.url, "probe", w.repo, id, hosted.HoldRecord{Location: loc}); err != nil {
		t.Fatal(err)
	}

	w.push(t)

	if got := hs.releasedIDs(); len(got) != 0 {
		t.Fatalf("a push released %v, want the hold taken after the drive-stopped row kept", got)
	}
	reg, _ := hosted.LoadHolds(w.url, "probe", w.repo)
	if reg[id].Location != loc || hs.holder(id).LocationID != loc {
		t.Errorf("the hold was dropped: registry %+v, server %+v", reg[id], hs.holder(id))
	}
}

// releaseHold lets go of a hold held here, and only that one.
func TestReleaseHoldOnlyReleasesAHoldHeldHere(t *testing.T) {
	hs := newFakeHoldServer()
	w := newHoldWorldWith(t, holdSyncToml, hs.routes)
	loc, err := hosted.LocationID(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	mine, theirs := createStoryID(t, "held here"), createStoryID(t, "held elsewhere")
	hs.setHolder(mine, loc, "box")
	hs.setHolder(theirs, otherLoc, "desk")
	for id, at := range map[string]string{mine: loc, theirs: otherLoc} {
		if err := hosted.RecordHold(w.url, "probe", w.repo, id, hosted.HoldRecord{Location: at}); err != nil {
			t.Fatal(err)
		}
	}
	site := holdSite{client: newHostedClient(t.Context(), w.url, w.repo), server: w.url, project: "probe", repoRoot: w.repo}

	if !releaseHold(t.Context(), t.Output(), site, mine) {
		t.Error("the hold held here was not released")
	}
	if releaseHold(t.Context(), t.Output(), site, theirs) {
		t.Error("a hold held elsewhere was released")
	}
	if got := hs.releasedIDs(); !slices.Equal(got, []string{mine}) {
		t.Errorf("released = %v, want only %s", got, mine)
	}
}
