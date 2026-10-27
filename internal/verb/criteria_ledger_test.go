package verb

import (
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// sty_99d3162b: a verdict's per-criterion entries are stored on the review row
// and on each panel seat, read back for the next presentation, and cleared of
// their quotations when the row goes stale.

func TestCriteriaRoundTripThroughTheReviewRow(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	entries := []CriterionVerdict{
		{Criterion: 0, Status: CriterionRejected, Finding: "general"},
		{Criterion: 1, Status: CriterionAccepted, Reviewed: "Q1", Text: "first", CarriedFrom: 2},
		{Criterion: 2, Status: CriterionUnchecked, Finding: "not looked at", Text: "second"},
	}
	seat := []CriterionVerdict{{Criterion: 1, Status: CriterionRejected, Finding: "seat finding", Reviewed: "SQ", Text: "first"}}
	rv := ReviewerVerdict{
		Skill: "pv-plan-review", Criteria: entries,
		Seats: []PanelSeat{{Seat: "s1", Criteria: seat}},
	}
	raw := reviewerPayload("backlog", "plan", rv, "")
	if raw == nil {
		t.Fatal("reviewerPayload returned nothing")
	}
	row := ledger.Entry{Kind: ledger.KindReviewReject, Payload: raw, CreatedAt: t0}

	got := PriorVerdictsFrom([]ledger.Entry{row}, "backlog", "plan", StaleExemption{})
	if len(got) != 1 {
		t.Fatalf("got %d prior verdicts, want 1", len(got))
	}
	if len(got[0].Criteria) != 3 || got[0].Criteria[1] != entries[1] || got[0].Criteria[2] != entries[2] {
		t.Errorf("criteria did not round-trip: %+v", got[0].Criteria)
	}
	if len(got[0].Seats) != 1 || len(got[0].Seats[0].Criteria) != 1 || got[0].Seats[0].Criteria[0] != seat[0] {
		t.Errorf("the seat's criteria did not round-trip: %+v", got[0].Seats)
	}

	// The same row, with ledger evidence recorded after its cycle ended, goes
	// stale: no entry keeps a quotation, but each keeps its status, finding, text.
	late := ledger.Entry{Kind: ledger.KindStoryDocAttached, Payload: []byte(`{}`), CreatedAt: time.Now().Add(time.Hour)}
	stale := PriorVerdictsFrom([]ledger.Entry{row, late}, "backlog", "plan", StaleExemption{})
	if len(stale) != 1 || !stale[0].ReviewedStale {
		t.Fatalf("the row should be stale: %+v", stale)
	}
	for _, e := range append(append([]CriterionVerdict(nil), stale[0].Criteria...), stale[0].Seats[0].Criteria...) {
		if e.Reviewed != "" || e.ReviewedTruncated {
			t.Errorf("a stale row must clear each entry's quotation: %+v", e)
		}
		if e.Status == "" || (e.Criterion != GeneralCriterion && e.Text == "") {
			t.Errorf("a stale row keeps status and text: %+v", e)
		}
	}
	if got[0].Criteria[1].Reviewed != "Q1" {
		t.Errorf("clearing a stale row's quotations must not write through the caller's entries: %+v", got[0].Criteria)
	}
}

func TestCriteriaAccept(t *testing.T) {
	acc := func(n int) CriterionVerdict { return CriterionVerdict{Criterion: n, Status: CriterionAccepted} }
	rej := func(n int) CriterionVerdict { return CriterionVerdict{Criterion: n, Status: CriterionRejected} }
	unc := func(n int) CriterionVerdict { return CriterionVerdict{Criterion: n, Status: CriterionUnchecked} }
	unm := func(n int) CriterionVerdict { return CriterionVerdict{Criterion: n, Status: CriterionUnmet} }
	cases := []struct {
		name    string
		top     bool
		entries []CriterionVerdict
		judged  []int
		want    bool
		failing []int
	}{
		{"no entries keeps the top-level accept", true, nil, []int{1, 2}, true, nil},
		{"no entries keeps the top-level reject", false, nil, []int{1, 2}, false, nil},
		{"every criterion accepted", true, []CriterionVerdict{acc(1), acc(2)}, []int{1, 2}, true, nil},
		{"a top-level accept with a rejected criterion", true, []CriterionVerdict{acc(1), rej(2)}, []int{1, 2}, false, []int{2}},
		{"a top-level accept with an unchecked criterion", true, []CriterionVerdict{acc(1), unc(2)}, []int{1, 2}, false, []int{2}},
		{"a top-level accept with a criterion omitted", true, []CriterionVerdict{acc(1)}, []int{1, 2}, false, []int{2}},
		{"a top-level accept with a general reject", true, []CriterionVerdict{acc(0), rej(0), acc(1)}, []int{1}, false, []int{0}},
		{"a top-level reject with every criterion accepted", false, []CriterionVerdict{acc(1), acc(2)}, []int{1, 2}, true, nil},
		{"a general accept is no obstacle", true, []CriterionVerdict{acc(0), acc(1)}, []int{1}, true, nil},
		{"an entry for a criterion not judged is ignored", true, []CriterionVerdict{acc(1), rej(9)}, []int{1}, true, nil},
		// sty_3b789fa5: unmet stands in for an accept only beside a general accept.
		{"an unmet criterion beside a general accept", true, []CriterionVerdict{acc(0), acc(1), unm(2)}, []int{1, 2}, true, nil},
		{"an unmet criterion beside a general accept ignores a top-level reject", false, []CriterionVerdict{acc(0), acc(1), unm(2)}, []int{1, 2}, true, nil},
		{"an unmet criterion with no general entry fails", true, []CriterionVerdict{acc(1), unm(2)}, []int{1, 2}, false, []int{2}},
		{"an unmet criterion beside a general reject fails with the general objection", true, []CriterionVerdict{rej(0), acc(1), unm(2)}, []int{1, 2}, false, []int{0, 2}},
		{"an unmet criterion beside a general unchecked fails", true, []CriterionVerdict{unc(0), acc(1), unm(2)}, []int{1, 2}, false, []int{2}},
		{"an unmet criterion beside another unchecked one fails", true, []CriterionVerdict{acc(0), unc(1), unm(2)}, []int{1, 2}, false, []int{1}},
		{"an unmet criterion beside a rejected one fails", true, []CriterionVerdict{acc(0), rej(1), unm(2)}, []int{1, 2}, false, []int{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, failing := CriteriaAccept(tc.top, tc.entries, tc.judged)
			if got != tc.want {
				t.Errorf("accept = %v, want %v", got, tc.want)
			}
			if len(failing) != len(tc.failing) {
				t.Fatalf("failing = %v, want %v", failing, tc.failing)
			}
			for i := range failing {
				if failing[i] != tc.failing[i] {
					t.Errorf("failing = %v, want %v", failing, tc.failing)
				}
			}
		})
	}
}

func TestCriteriaUnmetNamesTheUnmetJudgedCriteria(t *testing.T) {
	entries := []CriterionVerdict{
		{Criterion: 0, Status: CriterionAccepted},
		{Criterion: 1, Status: CriterionAccepted},
		{Criterion: 2, Status: CriterionUnmet},
		{Criterion: 4, Status: CriterionUnmet},
		{Criterion: 9, Status: CriterionUnmet},
	}
	got := CriteriaUnmet(entries, []int{1, 2, 3, 4})
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("CriteriaUnmet = %v, want [2 4]", got)
	}
}

func TestCriteriaFailureNamesTheCriteria(t *testing.T) {
	if got := CriteriaFailure([]int{0, 2, 5}); got != "the general objection, AC2, AC5" {
		t.Errorf("CriteriaFailure = %q", got)
	}
}
