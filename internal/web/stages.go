package web

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The story list's PROGRESS cell presents the STAGES a story has
// completed and how hard each gate was to clear — not one light per ledger row.
// A stage is named by the state a forward transition landed in; its totals are
// the gate's accepted and rejected ROUNDS (one attempt id however many reviewers
// voted, see ledger.EdgeRounds). Park and recover are not stages: a park is a
// note on the stage whose gate was in flight. Everything here is read-side
// presentation over rows the binary already wrote — no verdict is decided.

// stageVM is one entry of the PROGRESS cell.
type stageVM struct {
	Name     string
	State    string // done | current
	Accepted int    // accepted gate rounds on the edges into this stage
	Rejected int    // rejected gate rounds on the edges into this stage
	Parked   int    // parks taken while this stage's gate was in flight
	Title    string // tooltip: the totals spelled in words
	// Pending is the outgoing gate the story has presented and not yet passed:
	// its rounds since the story entered the stage. Set only on the current chip.
	Pending *gateRoundsVM
}

// gateRoundsVM is one gate's round counts, named by the edge they are for.
type gateRoundsVM struct {
	Accepted int
	Rejected int
	Edge     string // "from → to" the counts are for
	Title    string
}

// transition kinds, as the stage builder reads them.
const (
	trStage   = iota // a forward landing in a state — makes a stage
	trPark           // into the route's resume park
	trRecover        // out of the resume park
	trSink           // into a cancel sink: not a park, shown only as the final chip
)

type transition struct {
	pos      int // index into the (sorted) entries
	from, to string
	kind     int
}

// finished reports whether a story at status is past all work: a terminal
// success state or a cancel sink (reviewer-role, never the resume park). With a
// resolved route the Spec decides, so no state name is hardcoded here. A story
// whose category resolved no route (an empty Spec, a non-route workflow) cannot
// ask the Spec, so the DEGRADED fallback applies: the workitem lifecycle names
// done and cancelled — the same pair the rest of the binary reads as a
// lifecycle check.
func finished(spec wfdot.Spec, status string) bool {
	if len(spec.States) > 0 {
		return spec.IsTerminalState(status) || isSink(spec, status)
	}
	return status == workitem.StatusDone || status == workitem.StatusCancelled
}

// isSink is a cancel sink: a park-role state that is neither the resume park nor
// a terminal success marker (a terminal state is a stage, whatever its role).
func isSink(spec wfdot.Spec, name string) bool {
	return spec.IsParkState(name) && !spec.IsResumePark(name) && !spec.IsTerminalState(name)
}

// classifyTransitions labels every status_transition of es. With a resolved
// route the park is the route's own declaration (Spec.IsResumePark), its recover
// the transition out of it, and a cancel sink is neither. With no Spec the
// DEGRADED fallback reads the shape instead: A→X later followed by X→A is a
// park and its recover (the structure of "resumes to origin"); a never-recovered
// park is left to the current-status rule, which names the story's status.
func classifyTransitions(es []ledger.Entry, spec wfdot.Spec) []transition {
	var ts []transition
	for pos, e := range es {
		if e.Kind != ledger.KindStatusTransition {
			continue
		}
		lp := parseEdge(e)
		ts = append(ts, transition{pos: pos, from: lp.From, to: lp.To})
	}
	if len(spec.States) > 0 {
		for i := range ts {
			switch {
			case spec.IsResumePark(ts[i].to):
				ts[i].kind = trPark
			case spec.IsResumePark(ts[i].from):
				ts[i].kind = trRecover
			case isSink(spec, ts[i].to):
				ts[i].kind = trSink
			}
		}
		return ts
	}
	for i := range ts {
		if ts[i].kind != trStage || ts[i].from == ts[i].to {
			continue
		}
		for j := i + 1; j < len(ts); j++ {
			if ts[j].kind == trStage && ts[j].from == ts[i].to && ts[j].to == ts[i].from {
				ts[i].kind, ts[j].kind = trPark, trRecover
				break
			}
		}
	}
	return ts
}

func parseEdge(e ledger.Entry) lightPayload {
	var lp lightPayload
	_ = json.Unmarshal(e.Payload, &lp)
	return lp
}

// buildStages folds a story's ledger into the PROGRESS stages. Entries may arrive newest- or oldest-first; they are re-sorted here.
// Stages are in the order they were first reached — never numbered, never by
// route depth — so a story whose category resolved no route reads the same as
// one that did, and no stage appears twice. stepOf is consulted only to tell an
// on-route status from an unstarted one when the ledger is empty.
func buildStages(entries []ledger.Entry, status string, seatHeld bool, stepOf func(state string) int, spec wfdot.Spec) []stageVM {
	es := append([]ledger.Entry(nil), entries...)
	sortLedger(es)
	ts := classifyTransitions(es, spec)
	fin := finished(spec, status)

	var stages []stageVM
	at := map[string]int{} // stage name → index in stages
	type park struct{ target, within string }
	var parks []park
	lastEdgeTo, curName := "", ""
	offRoute := map[string]bool{} // edges of a park, recover or sink: not a stage's gate
	ti := 0
	for _, e := range es {
		switch e.Kind {
		case ledger.KindReviewAccept, ledger.KindReviewReject:
			lastEdgeTo = parseEdge(e).To
		case ledger.KindStatusTransition:
			t := ts[ti]
			ti++
			switch t.kind {
			case trPark:
				offRoute[t.from+"→"+t.to] = true
				// The park interrupted the gate in flight: note it on that gate's
				// target stage if the story ever reaches it, else on the stage the
				// story was in (else the state it left).
				target, within := lastEdgeTo, curName
				if within == "" {
					within = t.from
				}
				if target == "" {
					target = within
				}
				parks = append(parks, park{target, within})
				lastEdgeTo = ""
			case trRecover:
				offRoute[t.from+"→"+t.to] = true
				lastEdgeTo = ""
			case trSink:
				offRoute[t.from+"→"+t.to] = true
				lastEdgeTo = ""
			default:
				if _, ok := at[t.to]; !ok {
					at[t.to] = len(stages)
					stages = append(stages, stageVM{Name: t.to, State: "done"})
				}
				curName = t.to
				lastEdgeTo = ""
			}
		}
	}

	// Totals: the rounds on every edge into a stage, across parks and re-entries.
	seen := map[string]bool{}
	for _, e := range es {
		if e.Kind != ledger.KindReviewAccept && e.Kind != ledger.KindReviewReject {
			continue
		}
		lp := parseEdge(e)
		key := lp.From + "→" + lp.To
		i, ok := at[lp.To]
		if !ok || seen[key] || offRoute[key] || routeEdgeOff(spec, lp.From, lp.To) {
			continue
		}
		seen[key] = true
		a, r := ledger.EdgeRounds(es, lp.From, lp.To)
		stages[i].Accepted += a
		stages[i].Rejected += r
	}
	for _, p := range parks {
		if i, ok := at[p.target]; ok {
			stages[i].Parked++
		} else if i, ok := at[p.within]; ok {
			stages[i].Parked++
		}
	}

	// The current stage. A landed stage named by the status pulses in place; a
	// status with no landing stage (a park the story is still in, a story whose
	// ledger is missing, a cancel sink) gets its own chip. A finished story never
	// pulses — its last chip is simply done.
	entered := len(ts) > 0
	if i, ok := at[status]; ok {
		if !fin {
			stages[i].State = "current"
		}
	} else if fin {
		if entered {
			stages = append(stages, stageVM{Name: status, State: "done"})
		}
	} else if entered || (stepOf != nil && stepOf(status) > 0) {
		stages = append(stages, stageVM{Name: status, State: "current"})
	} else if seatHeld {
		stages = append(stages, stageVM{Name: status, State: "current", Title: "starting"})
	}
	for i := range stages {
		if stages[i].Title == "" {
			stages[i].Title = stageTitle(stages[i])
		}
	}
	if !fin {
		if p := pendingGate(es, ts, status); p != nil {
			for i := range stages {
				if stages[i].State == "current" {
					stages[i].Pending = p
				}
			}
		}
	}
	return stages
}

// routeEdgeOff reports whether a review edge is a park, recover or sink edge of
// the resolved route — its rounds belong to no stage's gate.
func routeEdgeOff(spec wfdot.Spec, from, to string) bool {
	if len(spec.States) == 0 {
		return false
	}
	return spec.IsResumePark(from) || spec.IsResumePark(to) || isSink(spec, to)
}

// pendingGate is the outgoing gate the story is waiting on: an edge out of
// status presented since the story entered it, reported with that edge's rounds
// since the entry. The entry gate is not reported here — its rounds are already
// in the totals of the stage it led into. No outgoing review rows, no result.
func pendingGate(es []ledger.Entry, ts []transition, status string) *gateRoundsVM {
	// The last transition into status that is not a recover: a recovery returns a
	// story to the state it left, so its entry gate is the one it first arrived by.
	enterPos := -1
	for _, t := range ts {
		if t.to == status && t.kind != trRecover {
			enterPos = t.pos
		}
	}
	var last *lightPayload
	for _, e := range es[enterPos+1:] {
		if e.Kind != ledger.KindReviewAccept && e.Kind != ledger.KindReviewReject {
			continue
		}
		if lp := parseEdge(e); lp.From == status {
			last = &lp
		}
	}
	if last == nil {
		return nil
	}
	a, r := ledger.EdgeRounds(es[enterPos+1:], status, last.To)
	if a == 0 && r == 0 {
		return nil
	}
	edge := status + " → " + last.To
	return &gateRoundsVM{Accepted: a, Rejected: r, Edge: edge,
		Title: edge + ": " + roundsWords(a, r)}
}

func stageTitle(s stageVM) string {
	parts := []string{}
	if w := roundsWords(s.Accepted, s.Rejected); w != "" {
		parts = append(parts, w)
	}
	switch {
	case s.Parked == 1:
		parts = append(parts, "parked once")
	case s.Parked > 1:
		parts = append(parts, fmt.Sprintf("parked %d times", s.Parked))
	}
	title := s.Name
	if s.State == "current" {
		title += " (current stage)"
	}
	if len(parts) > 0 {
		title += ": " + strings.Join(parts, ", ")
	}
	return title
}

// roundsWords spells round totals in words, so a number is never mistaken for a
// stage index.
func roundsWords(accepted, rejected int) string {
	var parts []string
	for _, p := range []struct {
		n    int
		word string
	}{{accepted, "accepted"}, {rejected, "rejected"}} {
		if p.n == 0 {
			continue
		}
		noun := "round"
		if p.n > 1 {
			noun = "rounds"
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", p.n, p.word, noun))
	}
	return strings.Join(parts, ", ")
}

// categorySpecOf resolves each category's derived-route Spec, memoised per
// category for one page load (an empty Spec when no route resolves — the read
// surface degrades, never refuses). The same shared front door as the depths
// categoryStepOf numbers by.
func categorySpecOf(docs []docindex.Doc) func(category string) wfdot.Spec {
	memo := map[string]wfdot.Spec{}
	return func(category string) wfdot.Spec {
		if s, ok := memo[category]; ok {
			return s
		}
		s := routeSpecFor(docs, category)
		memo[category] = s
		return s
	}
}
