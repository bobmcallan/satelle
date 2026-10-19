package verb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A caller that relays an objection (the headless drive) reads it off the error,
// not out of its message: a rejected presented edge is a GateRejection carrying
// each rejecting reviewer's notes, and an edge whose reject budget is spent is a
// RejectBudgetSpent carrying the last objection. Neither changes the prose.
func TestPresentedEdgeRefusalsAreTyped(t *testing.T) {
	r := newReadyRig(t, "ready", readyReviewers)
	it := newStoryOf(t, "feature", "typed refusals")
	r.g.verd = func(workitem.Item, string) verb.GateDecision { return fourReviewers("not good enough") }

	for i := 0; i < 3; i++ { // reject_budget = 3 rejected rounds
		_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
		var rej *verb.GateRejection
		if !errors.As(err, &rej) {
			t.Fatalf("round %d: want a GateRejection, got %T %v", i+1, err, err)
		}
		if rej.From != "backlog" || rej.To != "plan" || len(rej.Rejects) != 3 {
			t.Errorf("round %d: rejection = %+v, want backlog→plan with the three rejecting reviewers", i+1, rej)
		}
		notes := rej.Notes()
		for _, skill := range []string{"gate-a", "gate-b", "gate-c"} {
			if !strings.Contains(notes, skill+": not good enough ("+skill+")") {
				t.Errorf("Notes() must give each rejecting skill's notes, missing %s:\n%s", skill, notes)
			}
		}
		if strings.Contains(notes, "gate-d") {
			t.Errorf("an accepting reviewer's notes are not findings:\n%s", notes)
		}
		if !strings.Contains(err.Error(), "rejected by") {
			t.Errorf("the message must stay the historical prose, got %q", err.Error())
		}
	}

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	var spent *verb.RejectBudgetSpent
	if !errors.As(err, &spent) {
		t.Fatalf("a spent budget must be a RejectBudgetSpent, got %T %v", err, err)
	}
	if spent.Rounds != 3 || spent.Budget != 3 || spent.From != "backlog" || spent.To != "plan" || !strings.Contains(spent.Objection, "not good enough") {
		t.Errorf("spent = %+v", spent)
	}
	if !strings.Contains(err.Error(), "reject budget 3") || !strings.Contains(err.Error(), "decision needed") {
		t.Errorf("the message must stay the blocked-reason prose, got %q", err.Error())
	}
}

// ResumeParkName is the exported answer to "where does a story of this lane park".
func TestResumeParkNameNamesTheLanesParkState(t *testing.T) {
	newReadyRig(t, "ready", readyReviewers)
	it := newStoryOf(t, "feature", "where to park")
	if got := verb.ResumeParkName(t.Context(), it); got != "blocked" {
		t.Errorf("ResumeParkName = %q, want blocked", got)
	}
}
