package agentstep

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// A model re-flows the line breaks of a passage it quotes. A verdict that
// recorded per-criterion entries is matched by words, so the quotation still
// carries and a changed word still does not. A verdict that recorded none is
// matched exactly, as it always was (sty_99d3162b AC7).
func TestQuotationCarriesMatchesByWordsOnlyForVerdictsWithCriteria(t *testing.T) {
	const judged = "intro\nthe premise holds\n\tacross   two lines\noutro"
	withCriteria := []verb.CriterionVerdict{{Criterion: 1, Status: verb.CriterionAccepted}}
	for _, tc := range []struct {
		name     string
		reviewed string
		criteria []verb.CriterionVerdict
		want     bool
	}{
		{"criteria: newlines became spaces", "the premise holds across two lines", withCriteria, true},
		{"criteria: runs and tabs collapsed", "the premise   holds\t across two\nlines", withCriteria, true},
		{"criteria: exact", "the premise holds\n\tacross   two lines", withCriteria, true},
		{"criteria: a changed word", "the premise holds across three lines", withCriteria, false},
		{"criteria: words joined", "the premiseholds across two lines", withCriteria, false},
		{"no criteria: newlines became spaces", "the premise holds across two lines", nil, false},
		{"no criteria: exact", "the premise holds\n\tacross   two lines", nil, true},
	} {
		pv := PriorVerdict{Decision: "accept", Reviewed: tc.reviewed, Criteria: tc.criteria}
		if got := quotationCarries(pv, judged); got != tc.want {
			t.Errorf("%s: quotationCarries = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// On a reviewer_carry edge a reviewer whose prior accept recorded criteria and
// quoted the plan with its layout re-flowed is still carried; one that recorded
// no criteria and re-flowed its quotation is dispatched fresh, as before.
func TestCarryReflowedQuotationCarriesOnlyWithCriteria(t *testing.T) {
	const reflowed = "PLAN-B  and\nPLAN-C" // carryPlan holds "PLAN-B and PLAN-C"
	prior := []PriorVerdict{
		{Skill: "rev-a", Decision: "accept", Reviewed: reflowed},
		{Skill: "rev-b", Decision: "accept", Reviewed: reflowed,
			Criteria: []verb.CriterionVerdict{{Criterion: 1, Status: verb.CriterionAccepted, Reviewed: reflowed, Text: "x"}}},
		rejected("rev-c"),
	}
	for _, sc := range carrySchedulers {
		t.Run(sc.name, func(t *testing.T) {
			g, r := carryEngine(t, sc.par, sc.extra, prior, map[string]string{"rev-c": "reject"})
			dec := carryGate(t, g)
			if dec.Accept {
				t.Fatalf("rev-c rejects, so the edge is blocked: %+v", dec)
			}
			if n := r.sessionsFor("rev-b"); n != 0 {
				t.Errorf("rev-b (criteria recorded) was dispatched %d time(s), want it carried", n)
			}
			if b := reviewerOf(t, dec, "rev-b"); !b.Accept || b.CarriedFrom != 2 {
				t.Errorf("rev-b = %+v, want a carried accept from attempt 2", b)
			}
			if n := r.sessionsFor("rev-a"); n != 1 {
				t.Errorf("rev-a (no criteria) was dispatched %d time(s), want 1: an exact match is required", n)
			}
			if a := reviewerOf(t, dec, "rev-a"); a.CarriedFrom != 0 {
				t.Errorf("rev-a = %+v, want a fresh verdict", a)
			}
		})
	}
}

// The per-criterion settled match goes through the same words rule: a criterion
// whose quotation lost its line breaks is still settled, and one whose premise
// word changed is open. The recorded quotation stays as the model wrote it.
func TestSettledCriterionMatchesAReflowedQuotation(t *testing.T) {
	plan := "# Plan\n\n## Premise check\nPREMISE-1 holds\nover two lines. PREMISE-2 now differs. PREMISE-3 holds.\n\n" +
		"## Acceptance criteria\n1. first criterion\n2. second criterion\n3. third criterion\n\n## Risks\nnone\n"
	prior := PriorVerdict{
		Skill: "rev-a", Decision: "reject", Notes: "an objection", Attempt: 1,
		Criteria: []verb.CriterionVerdict{
			{Criterion: 1, Status: verb.CriterionAccepted, Reviewed: "PREMISE-1 holds over two lines.", Text: "first criterion"},
			{Criterion: 2, Status: verb.CriterionAccepted, Reviewed: "PREMISE-2 holds.", Text: "second criterion"},
			{Criterion: 3, Status: verb.CriterionAccepted, Reviewed: "PREMISE-3 holds.", Text: "third criterion"},
		},
	}
	f := newCritFixture(t, critOpts{carry: true, plan: plan, prior: []PriorVerdict{prior},
		solo: verdictJSON("accept", critEntry(2, "accept", ""))})
	dec, rv := f.gate(t)

	state, _, _ := f.solo.payload(t, 0)
	if len(state) != 3 || state[0].State != criterionSettled || state[1].State != criterionOpen || state[2].State != criterionSettled {
		t.Fatalf("AC1 settles on a reflowed quotation, AC2 (changed word) is open: %+v", state)
	}
	if !dec.Accept {
		t.Fatalf("the carried accepts and the fresh AC2 accept: %+v", rv)
	}
	if e := entryOf(t, rv.Criteria, 1); e.CarriedFrom != 1 || e.Reviewed != "PREMISE-1 holds over two lines." {
		t.Errorf("AC1 is carried with its quotation as recorded: %+v", e)
	}
}
