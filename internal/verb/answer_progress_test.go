package verb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_73224b86: a propose step that enables answer_progress refuses a
// re-presentation whose performer left the prior blocking findings unprogressed,
// before any gate runs. The fixture performer reports the progress fact itself;
// computing it is internal/agentstep's job (findings_test.go).

func answerProgressWF(budget int, minProgress string) map[string]string {
	knobs := "propose = true\nreject_budget = " + string(rune('0'+budget)) + "\n[readied.answer_progress]\nenabled = true\n"
	if minProgress != "" {
		knobs += "min_progress = " + minProgress + "\n"
	}
	return routeHalves(readinessDoneToml, readinessStepToml(knobs))
}

// twoSeatReject is a presentation where both seats of a panel reject.
func twoSeatReject() verb.GateDecision {
	return verb.GateDecision{Gated: true, Accept: false, Skill: "gate-a", Reviewers: []verb.ReviewerVerdict{
		{Skill: "gate-a", Accept: false, Notes: "seat one objects"},
		{Skill: "gate-b", Accept: false, Notes: "seat two objects"},
	}}
}

// progressRig rejects the first presentation with two seats, then has the
// performer report progress(n) on every later presentation (n is the 1-based
// presentation); a first presentation reports none.
func progressRig(t *testing.T, wf map[string]string, progress func(n int) *verb.FindingProgress) (*readinessRig, string) {
	t.Helper()
	r := newReadinessRig(t, wf, func(string, int) verb.GateDecision { return twoSeatReject() })
	performed := 0
	verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "plan" {
			return verb.DispatchResult{}, nil
		}
		performed++
		res := verb.DispatchResult{Dispatched: true, Agent: "planner", Skill: "plan"}
		if performed > 1 {
			res.Progress = progress(performed)
		}
		return res, nil
	}))
	it := newFeature(t)
	if err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"}); !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("setup: the first presentation must be rejected by the panel, got %v", err)
	}
	return r, it.ID
}

func offered() []string { return []string{"gate-a/seat-1#1", "gate-b/seat-2#1"} }

// AC1: nothing answered and nothing changed — refused before a gate, listing the ids.
func TestUnansweredFindingsRefuseBeforeTheGates(t *testing.T) {
	r, id := progressRig(t, answerProgressWF(5, ""), func(int) *verb.FindingProgress {
		return &verb.FindingProgress{Offered: offered(), Unprogressed: offered()}
	})
	gates := r.gater.calls["plan"]

	err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "plan"})
	for _, want := range append(offered(), "unanswered findings", "0 of 2", "required 1") {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q, got: %v", want, err)
		}
	}
	if got := r.gater.calls["plan"]; got != gates {
		t.Errorf("a refused presentation dispatches no reviewer: gate calls = %d, want %d", got, gates)
	}
	if got := statusOf(t, id).Status; got != "backlog" {
		t.Errorf("status = %q, want backlog", got)
	}
	rows := ledgerRows(t, id, ledger.KindPerformerReject)
	if len(rows) != 1 || !strings.Contains(string(rows[0].Payload), verb.UnansweredFindingsSkill) {
		t.Fatalf("want one performer_reject row for %s, got %v", verb.UnansweredFindingsSkill, rows)
	}
}

// AC1: min_progress is capped at the number offered, so one finding is enough
// to satisfy a threshold of two — and too few under a real threshold refuses.
func TestMinProgressIsCappedAtTheNumberOffered(t *testing.T) {
	cases := []struct {
		name     string
		min      string
		progress *verb.FindingProgress
		refused  bool
	}{
		{"answered one of two, default min 1", "", &verb.FindingProgress{Offered: offered(), Progressed: offered()[:1], Unprogressed: offered()[1:]}, false},
		{"answered none of two, default min 1", "", &verb.FindingProgress{Offered: offered(), Unprogressed: offered()}, true},
		{"one of two, min 2", "2", &verb.FindingProgress{Offered: offered(), Progressed: offered()[:1], Unprogressed: offered()[1:]}, true},
		{"two of two, min 2", "2", &verb.FindingProgress{Offered: offered(), Progressed: offered()}, false},
		{"one offered and progressed, min 2 (capped)", "2", &verb.FindingProgress{Offered: offered()[:1], Progressed: offered()[:1]}, false},
		{"one offered and not, min 2 (capped)", "2", &verb.FindingProgress{Offered: offered()[:1], Unprogressed: offered()[:1]}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, id := progressRig(t, answerProgressWF(5, c.min), func(int) *verb.FindingProgress { return c.progress })
			gates := r.gater.calls["plan"]

			err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "plan"})
			refused := strings.Contains(err.Error(), "unanswered findings")
			if refused != c.refused {
				t.Fatalf("refused = %v, want %v: %v", refused, c.refused, err)
			}
			if got, want := r.gater.calls["plan"], gates+1; refused == (got == want) {
				t.Errorf("gate calls = %d (was %d): the panel must run exactly when the presentation is not refused", got, gates)
			}
		})
	}
}

// AC2: a first presentation (no findings offered, no progress fact) runs the gates.
func TestNoFindingsOfferedRunsTheGates(t *testing.T) {
	r := newReadinessRig(t, answerProgressWF(5, ""), func(string, int) verb.GateDecision { return twoSeatReject() })
	it := newFeature(t)
	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if strings.Contains(err.Error(), "unanswered findings") || r.gater.calls["plan"] != 1 {
		t.Errorf("a first presentation must reach the gates: calls = %d, err = %v", r.gater.calls["plan"], err)
	}
}

// AC3: a refusal is a rejected round against reject_budget; at the budget the
// existing budget refusal parks the story with the unprogressed ids as its objection.
func TestUnansweredRefusalCountsAgainstTheRejectBudget(t *testing.T) {
	r, id := progressRig(t, answerProgressWF(2, ""), func(int) *verb.FindingProgress {
		return &verb.FindingProgress{Offered: offered(), Unprogressed: offered()}
	})
	if err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "plan"}); !strings.Contains(err.Error(), "unanswered findings") {
		t.Fatalf("round 2 must be refused as unanswered findings, got %v", err)
	}
	gates := r.gater.calls["plan"]

	err := dispatchErr(t, "story-set", map[string]any{"id": id, "status": "plan"})
	for _, want := range append(offered(), "reject budget 2", "edge rejected 2 times", verb.UnansweredFindingsSkill, "decision needed") {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the budget refusal must contain %q, got: %v", want, err)
		}
	}
	if got := r.gater.calls["plan"]; got != gates {
		t.Errorf("the budget refusal runs no gate: calls = %d, want %d", got, gates)
	}
	if n := len(ledgerRows(t, id, ledger.KindPerformerReject)); n != 1 {
		t.Errorf("the budget refusal is not itself a round: %d performer_reject rows, want 1", n)
	}
}
