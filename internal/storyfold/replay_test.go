package storyfold

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// Two versions of one repo's route. The newer inserts a plan step, so
// backlog → in_progress is an edge of the older route only.
const (
	replayDoneA = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled", gate = "cancel-review" }
`
	replayDoneB = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "planned", "coded", "closed"]
cancel = { state = "cancelled", gate = "cancel-review" }
`
	replayStepA = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["intent-review"]
requires = ["raised"]

[closed]
status = "done"
reviewers = ["done-review"]
terminal = true
requires = ["coded"]
`
	replayStepB = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
reviewers = ["plan-review"]
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["intent-review"]
requires = ["planned"]

[closed]
status = "done"
reviewers = ["done-review"]
terminal = true
requires = ["coded"]
`
)

func versionEvent(done, step string) ledger.Entry {
	hash := wfgovern.RouteHash(done, step)
	b, _ := json.Marshal(ledger.WorkflowSource{Hash: hash, Done: done, Step: step})
	return ledger.Entry{Kind: ledger.KindWorkflowVersion, Payload: b, WorkflowHash: hash}
}

func under(hash string, e ledger.Entry) ledger.Entry {
	e.WorkflowHash = hash
	return e
}

// sty_d84be8bc AC8: each transition is replayed against the workflow version its
// own event recorded. An edge present only in the older version replays as valid
// when it was recorded under that version, and an edge valid in neither version
// is reported as a replay break.
func TestReplayJudgesEachTransitionByItsRecordedVersion(t *testing.T) {
	a, b := versionEvent(replayDoneA, replayStepA), versionEvent(replayDoneB, replayStepB)
	hashA, hashB := a.WorkflowHash, b.WorkflowHash
	if hashA == hashB {
		t.Fatal("the two route versions must differ")
	}

	events := chain(
		created(),
		a,
		under(hashA, transition("backlog", "in_progress")), // an edge of A only
		b,
		under(hashB, transition("in_progress", "done")), // an edge of both
	)
	rep := Replay(events)
	if len(rep.Breaks) != 0 || rep.Transitions != 2 || rep.Checked != 2 {
		t.Fatalf("replay = %+v, want both transitions valid against their own versions", rep)
	}

	// The same edge recorded under the newer version is not one it declares.
	rep = Replay(chain(created(), a, b,
		under(hashB, transition("backlog", "in_progress"))))
	if len(rep.Breaks) != 1 || rep.Breaks[0].Reason != ReplayNoEdge || rep.Breaks[0].WorkflowHash != hashB {
		t.Fatalf("replay = %+v, want one no_edge break under version B", rep)
	}

	// An edge neither version declares.
	rep = Replay(chain(created(), a, b,
		under(hashA, transition("backlog", "done")),
		under(hashB, transition("done", "backlog"))))
	if len(rep.Breaks) != 2 {
		t.Fatalf("replay = %+v, want a break under each version", rep)
	}
	for i, br := range rep.Breaks {
		want := []string{hashA, hashB}[i]
		if br.Reason != ReplayNoEdge || br.WorkflowHash != want || br.StoryID != "sty_a" || br.EventID == "" {
			t.Errorf("break = %+v", br)
		}
	}
}

// sty_6f952dd0 AC2: a transition whose from is not the status the chain had
// reached at its place — an earlier attempt's event pulled after the story was
// done — does not change the fold, and replay reports it as a stale_from break.
func TestReplayReportsAStaleTransitionAsABreak(t *testing.T) {
	events := chain(created(), transition("backlog", "in_progress"), transition("in_progress", "done"),
		transition("plan", "in_progress"))
	rep := Replay(events)
	if len(rep.Breaks) != 1 || rep.Breaks[0].Reason != ReplayStaleFrom || rep.Breaks[0].From != "plan" || rep.Breaks[0].To != "in_progress" {
		t.Fatalf("replay = %+v, want one stale_from break for plan → in_progress", rep)
	}
	if rep.Breaks[0].EventID != events[3].ID {
		t.Errorf("break names %q, want the stale event %q", rep.Breaks[0].EventID, events[3].ID)
	}
}

func TestReplayReportsWhatItCannotCheckInsteadOfInventingIt(t *testing.T) {
	a := versionEvent(replayDoneA, replayStepA)

	// A transition that records a hash the chain holds no version for.
	rep := Replay(chain(created(), under("deadbeef", transition("backlog", "in_progress"))))
	if len(rep.Breaks) != 1 || rep.Breaks[0].Reason != ReplayNoVersion {
		t.Errorf("unknown version: %+v", rep)
	}
	// A transition from before the hash was recorded is counted, not checked.
	rep = Replay(chain(created(), transition("backlog", "in_progress")))
	if len(rep.Breaks) != 0 || rep.Unrecorded != 1 || rep.Checked != 0 {
		t.Errorf("unrecorded: %+v", rep)
	}
	// An archived source that does not hash to the version it is archived under.
	forged := a
	forged.WorkflowHash = "feedface"
	forged.Payload, _ = json.Marshal(ledger.WorkflowSource{Hash: "feedface", Done: replayDoneA, Step: replayStepA})
	rep = Replay(chain(created(), forged, under("feedface", transition("backlog", "in_progress"))))
	if len(rep.Breaks) != 1 || rep.Breaks[0].Reason != ReplayBadVersion {
		t.Errorf("forged source: %+v", rep)
	}
	// A legacy workflow document is counted, as its graph can no longer be read.
	legacy, _ := json.Marshal(ledger.WorkflowSource{Hash: wfgovern.LegacyHash("digraph {}"), Legacy: "digraph {}"})
	rep = Replay(chain(created(),
		ledger.Entry{Kind: ledger.KindWorkflowVersion, Payload: legacy},
		under(wfgovern.LegacyHash("digraph {}"), transition("backlog", "in_progress"))))
	if len(rep.Breaks) != 0 || rep.Unreplayable != 1 {
		t.Errorf("legacy: %+v", rep)
	}
}
