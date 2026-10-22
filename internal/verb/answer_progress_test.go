package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentstep"
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

// AC2, end to end: the panel's seats record their quotations on the ledger, the
// re-presenting performer is offered the findings built from that prior
// (agentstep.FindingsFrom over verb.PriorVerdicts), and its progress is the real
// agentstep computation over the plan it attaches — nothing injects the fact.
// The panel runs exactly when at least one finding is answered or has lost its
// quotation, and a plan that keeps every quotation unanswered is refused.
func TestProgressedFindingsRunTheGates(t *testing.T) {
	const (
		opusQuote = "the plan never says how AC2 is checked"
		grokQuote = "rollback is described but not bounded"
		oldPlan   = "# Plan\n" + opusQuote + ".\nAlso " + grokQuote + ".\n"
		opusID    = "gate-a/opus#1"
		grokID    = "gate-a/grok#1"
	)
	panelReject := verb.GateDecision{Gated: true, Accept: false, Skill: "gate-a", Reviewers: []verb.ReviewerVerdict{{
		Skill: "gate-a", Accept: false, Notes: "both seats object",
		Seats: []verb.PanelSeat{
			{Seat: "opus", Notes: "AC2 unchecked", Reviewed: opusQuote},
			{Seat: "grok", Notes: "rollback unbounded", Reviewed: grokQuote},
		},
	}}}
	answer := func(id string) agentartifact.Answer {
		return agentartifact.Answer{Finding: id, Answer: "adopted", Evidence: "see the plan"}
	}

	cases := []struct {
		name    string
		plan    string
		answers []agentartifact.Answer
		wantRun bool
	}{
		{"one finding answered, its quotation kept", oldPlan + "A new section.\n", []agentartifact.Answer{answer(opusID)}, true},
		{"one quotation removed, nothing answered", "# Plan\n" + opusQuote + ".\nRollback is now bounded to ten minutes.\n", nil, true},
		{"both findings answered", oldPlan + "Everything addressed.\n", []agentartifact.Answer{answer(opusID), answer(grokID)}, true},
		{"quotations kept and nothing answered", oldPlan + "A new section that answers nothing.\n", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReadinessRig(t, answerProgressWF(5, ""), func(_ string, n int) verb.GateDecision {
				if n == 1 {
					return panelReject
				}
				return acceptAll()
			})
			presentation := 0
			verb.SetExecutorDispatcher(dispatcherFunc(func(ctx context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
				if to != "plan" {
					return verb.DispatchResult{}, nil
				}
				presentation++
				res := verb.DispatchResult{Dispatched: true, Agent: "planner", Skill: "plan"}
				if presentation == 1 {
					call(t, "story-doc-attach", map[string]any{"story_id": it.ID, "name": "plan", "type": "plan", "body": oldPlan})
					return res, nil
				}
				verdicts, err := verb.PriorVerdicts(ctx, it.ID, it.Status, to, verb.StaleExemption{DocName: "plan", Performer: "planner"})
				if err != nil {
					t.Fatalf("prior verdicts: %v", err)
				}
				priors := make([]agentstep.PriorVerdict, 0, len(verdicts))
				for _, v := range verdicts {
					priors = append(priors, agentstep.PriorVerdictFrom(v))
				}
				findings := agentstep.FindingsFrom(it.ID, priors)
				var ids []string
				for _, f := range findings {
					ids = append(ids, f.ID)
				}
				if strings.Join(ids, ",") != opusID+","+grokID {
					t.Fatalf("the ledger prior must offer both seats' findings, got %v", ids)
				}
				call(t, "story-doc-attach", map[string]any{"story_id": it.ID, "name": "plan", "type": "plan", "body": c.plan})
				res.Answers = c.answers
				agentstep.RecordFindingProgress(&res, findings, agentstep.ProgressMaterial{
					Judged: it.Title + "\n" + it.Body + "\n" + c.plan, Output: c.plan, Prior: oldPlan, HasOutput: true,
				})
				return res, nil
			}))
			it := newFeature(t)
			if err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "plan"}); !strings.Contains(err.Error(), "rejected") {
				t.Fatalf("setup: the first presentation must be rejected by the panel, got %v", err)
			}
			gates := r.gater.calls["plan"]

			body, _ := json.Marshal(map[string]any{"id": it.ID, "status": "plan"})
			_, rerr := verb.Dispatch(context.Background(), "story-set", body)
			refused := rerr != nil && strings.Contains(rerr.Error(), "unanswered findings")
			if refused == c.wantRun {
				t.Fatalf("refused = %v, want %v (err %v)", refused, !c.wantRun, rerr)
			}
			if got, want := r.gater.calls["plan"], gates+1; c.wantRun != (got == want) {
				t.Errorf("gate calls = %d (was %d): the panel must run exactly when a finding progressed", got, gates)
			}
			rows := ledgerRows(t, it.ID, ledger.KindPerformerReject)
			if c.wantRun && len(rows) != 0 {
				t.Errorf("a presentation that runs the gates writes no performer_reject row, got %d", len(rows))
			}
			if !c.wantRun {
				for _, id := range []string{opusID, grokID} {
					if !strings.Contains(rerr.Error(), id) {
						t.Errorf("the refusal must list %s, got: %v", id, rerr)
					}
				}
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
