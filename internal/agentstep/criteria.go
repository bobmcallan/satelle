package agentstep

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Per-criterion verdicts (sty_99d3162b). The verdict's unit is the criterion: a
// seat may return one entry per criterion it judged, a settled criterion keeps
// its accept while its text is unchanged, and each panel seat is shown the other
// seats' open findings. The rubric for judging a criterion stays in the repo's
// skills and in the satelle-reviewer-verdict principle; this file records,
// carries and combines the entries.

// criterionNo is a criterion number on the wire. A model may send 2, "2" or
// "AC2"; anything else is -1, which parseCriteria drops. A dropped entry is a
// criterion the reviewer did not judge: it never lets the top-level decision
// stand in for the criteria (GateDecision.CriteriaSent, criteriaPrep.apply).
type criterionNo int

func (n *criterionNo) UnmarshalJSON(b []byte) error {
	*n = -1
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		if i, err := strconv.Atoi(num.String()); err == nil {
			*n = criterionNo(i)
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return nil
	}
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.EqualFold(s[:2], "AC") {
		s = strings.TrimSpace(s[2:])
	}
	if i, err := strconv.Atoi(s); err == nil {
		*n = criterionNo(i)
	}
	return nil
}

// rawCriterion is one entry of a reviewer's criteria[] as sent.
type rawCriterion struct {
	Criterion         criterionNo `json:"criterion"`
	Status            string      `json:"status"`
	Finding           string      `json:"finding,omitempty"`
	Reviewed          string      `json:"reviewed,omitempty"`
	ReviewedTruncated bool        `json:"reviewed_truncated,omitempty"`
}

// parseCriteria normalises a reviewer's criteria[]: a status that is not accept
// or reject is unchecked, a number below zero is dropped, a repeated number keeps
// its last entry, each quotation is bounded by applyReviewed, and the list is
// capped at maxCriteria. Text is not set here — the binary stamps it from the
// criteria judged (criteriaPrep.apply), never from the reviewer.
func parseCriteria(raw []rawCriterion) []verb.CriterionVerdict {
	if len(raw) == 0 {
		return nil
	}
	last := map[int]rawCriterion{}
	var order []int
	for _, r := range raw {
		n := int(r.Criterion)
		if n < 0 {
			continue
		}
		if _, ok := last[n]; !ok {
			order = append(order, n)
		}
		last[n] = r
	}
	sort.Ints(order)
	if len(order) > maxCriteria {
		order = order[:maxCriteria]
	}
	out := make([]verb.CriterionVerdict, 0, len(order))
	for _, n := range order {
		r := last[n]
		reviewed, truncated := applyReviewed(r.Reviewed)
		out = append(out, verb.CriterionVerdict{
			Criterion: n, Status: normaliseStatus(r.Status), Finding: r.Finding,
			Reviewed: reviewed, ReviewedTruncated: truncated,
		})
	}
	return out
}

func normaliseStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case verb.CriterionAccepted:
		return verb.CriterionAccepted
	case verb.CriterionRejected:
		return verb.CriterionRejected
	}
	return verb.CriterionUnchecked
}

// criteriaSourceStory names the story's own acceptance criteria as the source.
const criteriaSourceStory = "story"

// judgedCriteria is the criteria a gate's reviewer judges. When the performing
// step declares an output criteria section and its attached document carries
// numbered lines under it, those are the criteria — on a readiness edge the plan
// holds them before the story does (apply_criteria copies them at the next
// step). Otherwise they are the story's acceptance criteria. The section is
// found through the one owner of the performer's contract (Engine.StepPerformer
// → PerformerResolution), and extracted with the same function apply_criteria
// uses, so the judged text is the text that will be applied. The second result
// names the source for the payload.
func judgedCriteria(item workitem.Item, docs []DocState, perf verb.PerformerResolution) ([]agentartifact.Criterion, string) {
	if perf.CriteriaSection != "" && perf.OutputName != "" {
		for _, d := range docs {
			if d.Name != perf.OutputName {
				continue
			}
			if cs := agentartifact.NumberedCriteria(agentartifact.Section(d.Body, perf.CriteriaSection)); len(cs) > 0 {
				return cs, "doc:" + perf.OutputName + "#" + perf.CriteriaSection
			}
			break
		}
	}
	if cs := agentartifact.NumberedCriteria(item.AcceptanceCriteria); len(cs) > 0 {
		return cs, criteriaSourceStory
	}
	return nil, ""
}

// criteriaUnchanged reports whether a prior verdict that recorded per-criterion
// entries still stands whole: every criterion judged now has an accept entry
// recorded against the same text. A verdict with no entries made no claim about
// individual criteria, so it stands as it always did. quotationCarries checks the
// quotation; this checks that no criterion changed beneath an unchanged one.
func criteriaUnchanged(pv PriorVerdict, judged []agentartifact.Criterion) bool {
	if len(pv.Criteria) == 0 {
		return true
	}
	for _, j := range judged {
		if e, ok := entryFor(pv.Criteria, j.N); !ok || e.Status != verb.CriterionAccepted || e.Text != j.Text {
			return false
		}
	}
	return true
}

// criterionState is one judged criterion as a seat is shown it on a
// re-presentation: settled (its prior accept is carried and it need not be
// judged again) or open (judge it). PriorStatus and PriorFinding are what this
// seat said about it last time.
type criterionState struct {
	Criterion    int    `json:"criterion"`
	Text         string `json:"text"`
	State        string `json:"state"`
	PriorStatus  string `json:"prior_status,omitempty"`
	PriorFinding string `json:"prior_finding,omitempty"`
}

const (
	criterionSettled = "settled"
	criterionOpen    = "open"
)

// peerFinding is another seat's open finding — a reject or unchecked entry from
// its latest verdict — keyed by criterion (0 for an objection outside any
// criterion). A seat is never shown its own.
type peerFinding struct {
	Seat      string `json:"seat"`
	Criterion int    `json:"criterion"`
	Status    string `json:"status"`
	Finding   string `json:"finding,omitempty"`
}

// criteriaPrep is what one gate (or one panel seat) knows about the criteria it
// judges: the criteria and where they came from, and — on an edge that declares
// reviewer_carry — which of them are settled by this seat's latest verdict.
type criteriaPrep struct {
	judged []agentartifact.Criterion
	source string
	// carry reports that the edge declares reviewer_carry = "until_accept".
	carry bool
	// settled holds, by criterion number, the prior accept to carry; priorAttempt
	// is the attempt it was recorded on.
	settled      map[int]verb.CriterionVerdict
	priorAttempt int
}

func (c criteriaPrep) numbers() []int {
	out := make([]int, len(c.judged))
	for i, j := range c.judged {
		out[i] = j.N
	}
	return out
}

func (c criteriaPrep) text(n int) (string, bool) {
	for _, j := range c.judged {
		if j.N == n {
			return j.Text, true
		}
	}
	return "", false
}

func entryFor(entries []verb.CriterionVerdict, n int) (verb.CriterionVerdict, bool) {
	for _, e := range entries {
		if e.Criterion == n {
			return e, true
		}
	}
	return verb.CriterionVerdict{}, false
}

// latestOf is the last prior verdict of skill.
func latestOf(prior []PriorVerdict, skill string) (PriorVerdict, bool) {
	for i := len(prior) - 1; i >= 0; i-- {
		if prior[i].Skill == skill {
			return prior[i], true
		}
	}
	return PriorVerdict{}, false
}

// setState works out, from prior (the verdicts this gate or seat is shown), which
// criteria are settled and which are open, and returns the payload's state. A
// criterion is settled only on a reviewer_carry edge, and only when the latest
// prior verdict accepted it, recorded the same criterion text, and left a
// quotation that is still in the material judged now, by words (layout aside). Everything else —
// and criterion 0 — is open. The state is omitted when the edge neither carries
// nor has a prior verdict with criteria, so an ordinary gate's payload is
// unchanged.
func (c *criteriaPrep) setState(item workitem.Item, docs []DocState, prior []PriorVerdict, skill string) []criterionState {
	c.settled, c.priorAttempt = nil, 0
	if len(c.judged) == 0 {
		return nil
	}
	latest, have := latestOf(prior, skill)
	if !c.carry && !(have && len(latest.Criteria) > 0) {
		return nil
	}
	material := judgedText(item, docs)
	state := make([]criterionState, 0, len(c.judged))
	for _, j := range c.judged {
		st := criterionState{Criterion: j.N, Text: j.Text, State: criterionOpen}
		if e, ok := entryFor(latest.Criteria, j.N); have && ok {
			st.PriorStatus, st.PriorFinding = e.Status, e.Finding
			rests := PriorVerdict{Decision: "reject", Reviewed: e.Reviewed, ReviewedTruncated: e.ReviewedTruncated, ReviewedStale: latest.ReviewedStale}
			if e.Status == verb.CriterionAccepted {
				rests.Decision = "accept"
			}
			if c.carry && e.Text == j.Text && quotationCarries(rests, material) {
				st.State = criterionSettled
				if c.settled == nil {
					c.settled = map[int]verb.CriterionVerdict{}
				}
				c.settled[j.N] = e
				c.priorAttempt = latest.Attempt
			}
		}
		state = append(state, st)
	}
	return state
}

// peerFindingsFor is the other seats' open findings, from the latest row of skill
// that recorded seats. A peer with criteria contributes its reject and unchecked
// entries; a peer that rejected without criteria contributes its notes as
// criterion 0. The seat's own findings, and an unavailable seat, contribute
// nothing.
func peerFindingsFor(prior []PriorVerdict, skill, seat string) []peerFinding {
	for i := len(prior) - 1; i >= 0; i-- {
		if prior[i].Skill != skill || len(prior[i].Seats) == 0 {
			continue
		}
		var out []peerFinding
		for _, s := range prior[i].Seats {
			if s.Seat == seat || s.Unavailable {
				continue
			}
			if len(s.Criteria) == 0 {
				if !s.Accept && strings.TrimSpace(s.Notes) != "" {
					out = append(out, peerFinding{Seat: s.Seat, Criterion: verb.GeneralCriterion, Status: verb.CriterionRejected, Finding: s.Notes})
				}
				continue
			}
			for _, e := range s.Criteria {
				if e.Status != verb.CriterionAccepted {
					out = append(out, peerFinding{Seat: s.Seat, Criterion: e.Criterion, Status: e.Status, Finding: e.Finding})
				}
			}
		}
		return out
	}
	return nil
}

// apply settles a verdict that carries criteria[] against the criteria judged:
// entries for a number that is neither judged nor 0 are dropped, each remaining
// entry's text is stamped from the judged criteria, each settled criterion's
// prior accept replaces whatever the seat returned for it (carried_from naming
// the attempt), and the decision is then derived from the criteria
// (verb.CriteriaAccept) — the top-level decision is recorded but does not decide.
// A verdict with no criteria[] and nothing settled is untouched, so it is decided,
// stored and carried exactly as it was before per-criterion verdicts existed.
// When criteria are settled, the verdict records each one's carried accept even
// if the seat sent no criteria[] (the carried accept does not depend on the seat's
// output); and when no judged criterion is left open, the carried accepts decide —
// the seat was shown nothing to judge, so its top-level reject can only be of a
// settled criterion or a general objection it did not itemise.
func (c criteriaPrep) apply(dec *verb.GateDecision) {
	sent := dec != nil && (len(dec.Criteria) > 0 || dec.CriteriaSent)
	if dec == nil || (!sent && len(c.settled) == 0) {
		return
	}
	top := dec.Accept
	entries := make([]verb.CriterionVerdict, 0, len(dec.Criteria))
	for _, e := range dec.Criteria {
		if e.Criterion == verb.GeneralCriterion {
			e.Text = ""
			entries = append(entries, e)
			continue
		}
		if text, ok := c.text(e.Criterion); ok {
			e.Text = text
			entries = append(entries, e)
		}
	}
	for n, prior := range c.settled {
		text, _ := c.text(n)
		carried := verb.CriterionVerdict{
			Criterion: n, Status: verb.CriterionAccepted, Finding: prior.Finding,
			Reviewed: prior.Reviewed, ReviewedTruncated: prior.ReviewedTruncated,
			Text: text, CarriedFrom: c.priorAttempt,
		}
		replaced := false
		for i := range entries {
			if entries[i].Criterion == n {
				entries[i], replaced = carried, true
			}
		}
		if !replaced {
			entries = append(entries, carried)
		}
	}
	if len(entries) == 0 {
		// criteria[] was sent but nothing in it names a judged criterion (an
		// unparseable number, or one that was not judged). The verdict still decides
		// from its criteria, so every judged criterion is recorded unchecked rather
		// than the top-level decision standing for entries that never existed.
		for _, j := range c.judged {
			entries = append(entries, verb.CriterionVerdict{
				Criterion: j.N, Status: verb.CriterionUnchecked, Text: j.Text,
				Finding: "criteria[] carried no usable entry for this criterion",
			})
		}
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].Criterion < entries[b].Criterion })
	dec.Criteria = entries
	if !sent && len(c.settled) < len(c.judged) {
		// No criteria[] and some criterion still open: the carried accepts are
		// recorded, but nothing says what the seat judged of the open ones, so the
		// top-level decision stands as it always did.
		return
	}
	accept, failing := verb.CriteriaAccept(top, entries, c.numbers())
	if len(c.judged) == 0 {
		// Nothing numbered to judge: the top-level decision stands, and only a
		// general objection can overrule an accept.
		accept, failing = top, nil
		if e, ok := entryFor(entries, verb.GeneralCriterion); ok && e.Status == verb.CriterionRejected {
			accept, failing = false, []int{verb.GeneralCriterion}
		}
	}
	dec.Accept = accept
	switch {
	case top && !accept:
		dec.Notes = prefixNotes("criteria not accepted: "+verb.CriteriaFailure(failing), dec.Notes)
	case !top && accept:
		dec.Notes = prefixNotes("criteria accepted; top-level reject cleared by carried criteria", dec.Notes)
	}
}

func prefixNotes(prefix, notes string) string {
	if strings.TrimSpace(notes) == "" {
		return prefix
	}
	return fmt.Sprintf("%s — %s", prefix, notes)
}

// carriedCriteria is entries as a carried verdict records them: each reused from
// attempt.
func carriedCriteria(entries []verb.CriterionVerdict, attempt int) []verb.CriterionVerdict {
	if len(entries) == 0 {
		return nil
	}
	out := append([]verb.CriterionVerdict(nil), entries...)
	for i := range out {
		out[i].CarriedFrom = attempt
	}
	return out
}

// criterionCombine is one criterion's tally across a panel's seats, for the
// combine check: how many seats accepted, rejected or left it unchecked.
type criterionCombine struct {
	Criterion int `json:"criterion"`
	Accepts   int `json:"accepts"`
	Rejects   int `json:"rejects"`
	Unchecked int `json:"unchecked"`
}

// combineCriteria tallies the seats' entries by criterion, ascending. Seats that
// recorded no criteria contribute nothing.
func combineCriteria(seats [][]verb.CriterionVerdict) []criterionCombine {
	tally := map[int]*criterionCombine{}
	for _, entries := range seats {
		for _, e := range entries {
			t := tally[e.Criterion]
			if t == nil {
				t = &criterionCombine{Criterion: e.Criterion}
				tally[e.Criterion] = t
			}
			switch e.Status {
			case verb.CriterionAccepted:
				t.Accepts++
			case verb.CriterionRejected:
				t.Rejects++
			default:
				t.Unchecked++
			}
		}
	}
	out := make([]criterionCombine, 0, len(tally))
	for _, t := range tally {
		out = append(out, *t)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Criterion < out[b].Criterion })
	return out
}
