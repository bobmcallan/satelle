package verb

import "fmt"

// Per-criterion verdicts (sty_99d3162b). A reviewer seat may return one entry
// per acceptance criterion it judged, so what was checked is recorded rather than
// hoped for. The rubric for judging a criterion stays in the repo's skills; the
// binary records, carries and combines the entries.

// Criterion statuses. Anything a reviewer sends that is not accept or reject is
// recorded as unchecked, and unchecked never counts as accept.
const (
	CriterionAccepted  = "accept"
	CriterionRejected  = "reject"
	CriterionUnchecked = "unchecked"
)

// GeneralCriterion is the entry number for an objection that belongs to no
// single criterion. It has no text, is never settled or carried, and is judged
// fresh every round.
const GeneralCriterion = 0

// CriterionVerdict is one reviewer's verdict on one criterion. Text is stamped
// by the binary from the criteria judged — never taken from the reviewer — so a
// later presentation can tell whether the criterion changed. Reviewed is the
// quotation the entry rests on, bounded by the same ceiling as the top-level
// one. CarriedFrom is the attempt whose accept this entry reuses; zero for an
// entry judged this round.
type CriterionVerdict struct {
	Criterion         int    `json:"criterion"`
	Status            string `json:"status"`
	Finding           string `json:"finding,omitempty"`
	Reviewed          string `json:"reviewed,omitempty"`
	ReviewedTruncated bool   `json:"reviewed_truncated,omitempty"`
	Text              string `json:"text,omitempty"`
	CarriedFrom       int    `json:"carried_from,omitempty"`
}

// CriteriaAccept is the one rule for what a verdict that carries criteria
// decides. With no entries it is the top-level decision, unchanged. Otherwise
// the verdict accepts only when every judged criterion has an accept entry and
// no general (criterion 0) entry rejects; the top-level decision is recorded but
// does not decide. The second result is the judged criteria that do not have an
// accept entry, in judged order, plus 0 when a general entry rejects.
func CriteriaAccept(top bool, entries []CriterionVerdict, judged []int) (bool, []int) {
	if len(entries) == 0 {
		return top, nil
	}
	status := make(map[int]string, len(entries))
	for _, e := range entries {
		status[e.Criterion] = e.Status
	}
	var failing []int
	if status[GeneralCriterion] == CriterionRejected {
		failing = append(failing, GeneralCriterion)
	}
	for _, n := range judged {
		if status[n] != CriterionAccepted {
			failing = append(failing, n)
		}
	}
	return len(failing) == 0, failing
}

// CriteriaFailure names the failing criteria for a verdict's notes: "AC2, AC5",
// with "the general objection" for criterion 0.
func CriteriaFailure(failing []int) string {
	out := ""
	for i, n := range failing {
		if i > 0 {
			out += ", "
		}
		if n == GeneralCriterion {
			out += "the general objection"
			continue
		}
		out += fmt.Sprintf("AC%d", n)
	}
	return out
}

// withoutQuotations is entries with each Reviewed cleared, as markStale clears a
// stale verdict's quotation. It never writes through the caller's slice.
func withoutQuotations(entries []CriterionVerdict) []CriterionVerdict {
	if len(entries) == 0 {
		return entries
	}
	out := append([]CriterionVerdict(nil), entries...)
	for i := range out {
		out[i].Reviewed, out[i].ReviewedTruncated = "", false
	}
	return out
}
