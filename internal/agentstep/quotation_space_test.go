package agentstep

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// A model re-flows the line breaks of a passage it quotes. Whether or not the
// verdict recorded per-criterion entries, the quotation is matched by words, so
// a re-flowed quotation still carries and a changed word still does not. The
// match normalises its own copies: the quotation stays exactly as recorded.
func TestQuotationCarriesMatchesByWords(t *testing.T) {
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
		{"no criteria: newlines became spaces", "the premise holds across two lines", nil, true},
		{"no criteria: runs and tabs collapsed", "the premise   holds\t across two\nlines", nil, true},
		{"no criteria: exact", "the premise holds\n\tacross   two lines", nil, true},
		{"no criteria: a changed word", "the premise holds across three lines", nil, false},
		{"no criteria: words joined", "the premiseholds across two lines", nil, false},
	} {
		pv := PriorVerdict{Decision: "accept", Reviewed: tc.reviewed, Criteria: tc.criteria}
		if got := quotationCarries(pv, judged); got != tc.want {
			t.Errorf("%s: quotationCarries = %v, want %v", tc.name, got, tc.want)
		}
		if pv.Reviewed != tc.reviewed {
			t.Errorf("%s: the quotation was changed to %q", tc.name, pv.Reviewed)
		}
	}
}

// On a reviewer_carry edge a reviewer whose prior accept quoted the plan with its
// layout re-flowed is carried, with or without recorded criteria; one that quoted
// it with a word changed is dispatched fresh. The carried quotation stays as the
// reviewer wrote it.
func TestCarryReflowedQuotationCarries(t *testing.T) {
	const reflowed = "PLAN-B  and\nPLAN-C" // carryPlan holds "PLAN-B and PLAN-C"
	prior := []PriorVerdict{
		{Skill: "rev-a", Decision: "accept", Reviewed: reflowed},
		{Skill: "rev-b", Decision: "accept", Reviewed: reflowed,
			Criteria: []verb.CriterionVerdict{{Criterion: 1, Status: verb.CriterionAccepted, Reviewed: reflowed, Text: "x"}}},
		{Skill: "rev-c", Decision: "accept", Reviewed: "PLAN-B but PLAN-C"},
	}
	for _, sc := range carrySchedulers {
		t.Run(sc.name, func(t *testing.T) {
			g, r := carryEngine(t, sc.par, sc.extra, prior, map[string]string{"rev-c": "reject"})
			dec := carryGate(t, g)
			if dec.Accept {
				t.Fatalf("rev-c rejects, so the edge is blocked: %+v", dec)
			}
			for _, name := range []string{"rev-a", "rev-b"} {
				if n := r.sessionsFor(name); n != 0 {
					t.Errorf("%s was dispatched %d time(s), want it carried", name, n)
				}
				if v := reviewerOf(t, dec, name); !v.Accept || v.CarriedFrom == 0 || v.Reviewed != reflowed {
					t.Errorf("%s = %+v, want a carried accept with its quotation as recorded", name, v)
				}
			}
			if n := r.sessionsFor("rev-c"); n != 1 {
				t.Errorf("rev-c (a changed word) was dispatched %d time(s), want 1", n)
			}
			if c := reviewerOf(t, dec, "rev-c"); c.CarriedFrom != 0 {
				t.Errorf("rev-c = %+v, want a fresh verdict", c)
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
