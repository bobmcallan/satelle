package agentstep

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// seatPriorVerdicts projects an edge's prior verdicts (numbered over the full
// history) to what one panel seat is shown: its own verdicts. Each row of skill
// that records seats becomes this seat's own entry on that row — its decision,
// notes and reviewed quotation, never the combined row's and never another
// seat's — and a row that records no entry for this seat is dropped. A seat that
// was unavailable becomes a reject marked Unavailable with no quotation, so
// quotationCarries refuses it and the seat is judged in full. A row of skill
// that records no seats (written before seats carried a quotation) and rows of
// other skills pass through unchanged. Attempt, CreatedAt and the staleness
// marks of the parent row are kept.
func seatPriorVerdicts(all []PriorVerdict, skill, seat string) []PriorVerdict {
	out := make([]PriorVerdict, 0, len(all))
	for _, v := range all {
		if v.Skill != skill || len(v.Seats) == 0 {
			out = append(out, v)
			continue
		}
		rec, ok := seatRecord(v.Seats, seat)
		if !ok {
			continue
		}
		own := PriorVerdict{
			Skill:         v.Skill,
			Decision:      "reject",
			Notes:         rec.Notes,
			CreatedAt:     v.CreatedAt,
			Attempt:       v.Attempt,
			ReviewedStale: v.ReviewedStale,
			EvidenceSince: v.EvidenceSince,
		}
		if rec.Unavailable {
			own.Unavailable = true
		} else {
			if rec.Accept {
				own.Decision = "accept"
			}
			own.Reviewed, own.ReviewedTruncated = rec.Reviewed, rec.ReviewedTruncated
			own.Criteria = rec.Criteria
		}
		out = append(out, own)
	}
	return out
}

// seatPrep is prep as one seat opens it: the same material, with the seat's own
// prior verdicts in place of the edge's, both whole (material) and windowed
// (inline payload). prep is a copy; the shared prep is not touched.
//
// The seat is also shown which criteria its own latest verdict settled, and the
// other seats' open findings (sty_99d3162b) — never its own.
func seatPrep(prep reviewerPrep, skill, seat string) reviewerPrep {
	peers := peerFindingsFor(prep.material.PriorVerdicts, skill, seat)
	own := seatPriorVerdicts(prep.material.PriorVerdicts, skill, seat)
	prep.material.PriorVerdicts = own
	prep.tp.PriorVerdicts = windowPriorVerdicts(own)
	prep.tp.CriteriaState = prep.criteria.setState(prep.tp.Story, prep.material.Docs, own, skill)
	prep.tp.PeerFindings = peers
	return prep
}

// seatRecord finds seat's entry in a panel verdict's seats.
func seatRecord(seats []verb.PanelSeat, seat string) (verb.PanelSeat, bool) {
	for _, s := range seats {
		if s.Seat == seat {
			return s, true
		}
	}
	return verb.PanelSeat{}, false
}

// latestSeatEntry is seat's entry on the LATEST row of skill — the verdict a
// carry decision rests on. Only the latest row counts: it alone is checked for
// staleness, and a seat the latest row does not record is judged in full rather
// than carried from an older round.
func latestSeatEntry(all []PriorVerdict, skill, seat string) (PriorVerdict, bool) {
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Skill != skill {
			continue
		}
		own := seatPriorVerdicts(all[i:i+1], skill, seat)
		if len(own) == 0 {
			return PriorVerdict{}, false
		}
		return own[0], true
	}
	return PriorVerdict{}, false
}

// carriedSeat is one panel seat whose earlier accept is carried: idx is its
// position in the panel, run the record to fold in its place.
type carriedSeat struct {
	idx int
	run panelSeatRun
}

// carriedSeats splits panel into the seats whose latest verdict is an accept
// that quotationCarries against the material judged now, and the positions that
// must be dispatched. Nothing is carried unless the edge declares
// reviewer_carry = "until_accept".
func (g *Engine) carriedSeats(ctx context.Context, item workitem.Item, toStatus, skill string, panel []string, prep reviewerPrep) (carried []carriedSeat, fresh []int) {
	prior := prep.material.PriorVerdicts
	if len(prior) == 0 || g.edgeCarry(ctx, item, item.Status, toStatus) != wfdot.CarryUntilAccept {
		return nil, allPositions(len(panel))
	}
	judged := judgedText(item, prep.material.Docs)
	attempt := len(prior) + 1
	for i, seat := range panel {
		pv, ok := latestSeatEntry(prior, skill, seat)
		if !ok || !quotationCarries(pv, judged) || !criteriaUnchanged(pv, prep.criteria.judged) {
			fresh = append(fresh, i)
			continue
		}
		carried = append(carried, carriedSeat{idx: i, run: panelSeatRun{record: verb.PanelSeat{
			Seat: seat, Accept: true, Reviewed: pv.Reviewed, Attempt: attempt, CarriedFrom: pv.Attempt,
			Criteria: carriedCriteria(pv.Criteria, pv.Attempt),
			Notes:    fmt.Sprintf("carried from attempt %d: quotation unchanged", pv.Attempt),
		}}})
	}
	return carried, fresh
}

func allPositions(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// runPanel runs a panel skill's seats and folds them through the combine check.
// Under reviewer_carry a seat whose accept still rests on unchanged words is not
// dispatched while another seat is: its record is carried into the fold, so the
// combine check still judges the whole panel. Once the fold accepts, the carried
// seats are dispatched fresh and the panel is folded again — the verdict that
// advances the edge holds a fresh judgement from every seat. carryRound owns
// that sequence, as it does for the reviewers of an edge.
func (g *Engine) runPanel(ctx context.Context, item workitem.Item, toStatus, skill string, ref reviewerRef, command string, prep reviewerPrep) (verb.GateDecision, error) {
	panel := ref.panel
	carried, fresh := g.carriedSeats(ctx, item, toStatus, skill, panel, prep)
	runs := make([]panelSeatRun, len(panel))
	carriedIdx := make([]int, len(carried))
	for j, c := range carried {
		runs[c.idx] = c.run
		carriedIdx[j] = c.idx
	}
	fold := func() (verb.GateDecision, error) {
		return g.foldPanel(ctx, skill, ref.combine, command, panel, runs)
	}
	dec, handled, err := carryRound(carriedIdx, fresh,
		func(idx []int) (verb.GateDecision, error) {
			sections := make([]string, len(idx))
			for k, i := range idx {
				sections[k] = panel[i]
			}
			for k, r := range g.runPanelSeats(ctx, item, toStatus, skill, sections, prep, ref.parallel) {
				runs[idx[k]] = r
			}
			return fold()
		},
		func(dec verb.GateDecision) bool { return dec.Gated && !dec.Accept },
		func(dec verb.GateDecision) verb.GateDecision {
			for _, c := range carried {
				g.telemetryEvent(ctx, item.ID, "reviewer", "gate-carried", map[string]any{
					"skill": skill, "seat": c.run.record.Seat, "to": toStatus, "carried_from": c.run.record.CarriedFrom,
				})
			}
			return dec
		},
		// The confirming fold already holds every seat, the first pass's fresh
		// runs and the carried seats' fresh ones, so it is the whole decision.
		func(_, confirm verb.GateDecision) verb.GateDecision { return confirm })
	if handled {
		return dec, err
	}
	return g.foldPanel(ctx, skill, ref.combine, command, panel, g.runPanelSeats(ctx, item, toStatus, skill, panel, prep, ref.parallel))
}
