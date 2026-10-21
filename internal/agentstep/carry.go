package agentstep

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Carried reviewer verdicts (sty_59720b8b). A step that declares
// `reviewer_carry = "until_accept"` does not re-dispatch a reviewer whose
// earlier accept still rests on words that appear, unchanged, in the material it
// would judge — while any other reviewer is still being dispatched. The carried
// accept is recorded as a review_accept row carrying carried_from, with no
// agent_invocation. Once every dispatched reviewer accepts, the carried
// reviewers are dispatched fresh in the same call, so the round that advances
// the edge holds a fresh verdict from every reviewer.

// edgeCarry returns the active workflow's reviewer_carry policy for the from→to
// edge: "" or wfdot.CarryUntilAccept. Empty on any resolution failure — carry is
// an opt-in optimisation and must never be what breaks or changes a gate.
func (g *Engine) edgeCarry(ctx context.Context, item workitem.Item, from, to string) string {
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		return ""
	}
	for _, tr := range spec.Transitions {
		if tr.From == from && tr.To == to {
			return tr.ReviewerCarry
		}
	}
	return ""
}

// staleExemption is what the edge's step lets a prior verdict survive: nothing
// unless the step declares reviewer_carry, else the performer's output document
// and its directed messages. The engine is the only reader of the route key.
func (g *Engine) staleExemption(ctx context.Context, item workitem.Item, toStatus string) verb.StaleExemption {
	if g.edgeCarry(ctx, item, item.Status, toStatus) != wfdot.CarryUntilAccept {
		return verb.StaleExemption{}
	}
	perf, err := g.StepPerformer(ctx, item, toStatus)
	if err != nil || !perf.Dispatched {
		return verb.StaleExemption{}
	}
	return verb.StaleExemption{DocName: perf.OutputName, Performer: perf.Agent}
}

// carriedRef is one reviewer whose earlier accept is carried: idx is its
// position in the edge's ordered list, verdict the row to record.
type carriedRef struct {
	idx     int
	verdict verb.ReviewerVerdict
}

// judgedText is the material a carried quotation is checked against: the story
// card and every document fillPayloadDocs retained for the reviewer.
func judgedText(item workitem.Item, docs []DocState) string {
	var b strings.Builder
	b.WriteString(item.Title)
	b.WriteByte('\n')
	b.WriteString(item.Body)
	b.WriteByte('\n')
	b.WriteString(item.AcceptanceCriteria)
	for _, d := range docs {
		b.WriteByte('\n')
		b.WriteString(d.Body)
	}
	return b.String()
}

// quotationCarries is the one rule for whether a prior verdict's accept can be
// carried: an accept with a complete, non-stale quotation that appears verbatim
// in the material judged now. A reviewer's latest verdict and a panel seat's
// latest verdict (seatPriorVerdicts) are both held to it; an unavailable seat is
// projected to a reject, so it never carries.
func quotationCarries(pv PriorVerdict, judged string) bool {
	return pv.Decision == "accept" && !pv.ReviewedStale && !pv.ReviewedTruncated &&
		strings.TrimSpace(pv.Reviewed) != "" && strings.Contains(judged, pv.Reviewed)
}

// carryRound is the one sequence of a carried round, over units that are
// reviewers (gateCarrying) or panel seats (runReviewerRef). carried and fresh
// partition the units' positions. handled is false when nothing is carried, or
// nothing is dispatched (carry is moot) — the caller then dispatches every unit
// as it would without the key. Otherwise the fresh units are dispatched first.
// A rejection decides the round and withCarried records the carried units beside
// it. An acceptance dispatches the carried units fresh and merges the two
// passes, so the round that advances holds a fresh verdict from every unit.
func carryRound[R any](carried, fresh []int, dispatch func(idx []int) (R, error), rejected func(R) bool, withCarried func(R) R, merge func(first, confirm R) R) (res R, handled bool, err error) {
	if len(carried) == 0 || len(fresh) == 0 {
		return res, false, nil
	}
	first, err := dispatch(fresh)
	if err != nil {
		return first, true, err
	}
	if rejected(first) {
		return withCarried(first), true, nil
	}
	confirm, err := dispatch(carried)
	return merge(first, confirm), true, err
}

// carryable splits ordered into the reviewers whose latest verdict on the edge
// is an accept with a complete, non-stale quotation found verbatim in the
// current material, and the indexes that must be dispatched. A reject, a missing
// or truncated or stale quotation, a changed quotation, a functional check and a
// panel are always dispatched.
func (g *Engine) carryable(ctx context.Context, item workitem.Item, toStatus string, ordered []reviewerRef, sysStart int) (carried []carriedRef, fresh []int) {
	all := make([]int, len(ordered))
	for i := range ordered {
		all[i] = i
	}
	if g.priorVerdicts == nil {
		return nil, all
	}
	prior := g.priorVerdicts(ctx, item.ID, item.Status, toStatus, g.staleExemption(ctx, item, toStatus))
	latest := make(map[string]int, len(prior))
	for i, v := range prior {
		latest[v.Skill] = i
	}
	var mat reviewerMaterial
	var tp transitionPayload
	g.fillPayloadDocs(ctx, item.ID, &tp, &mat)
	judged := judgedText(item, mat.Docs)
	for i, ref := range ordered {
		pi, ok := latest[ref.skill]
		if !ok || ref.skill == "" || len(ref.panel) > 0 || ref.combine != "" {
			fresh = append(fresh, i)
			continue
		}
		pv := prior[pi]
		if !quotationCarries(pv, judged) {
			fresh = append(fresh, i)
			continue
		}
		body, err := g.skillBody(ctx, ref.skill)
		if err != nil || skillCheck(body) != "" {
			fresh = append(fresh, i)
			continue
		}
		carried = append(carried, carriedRef{idx: i, verdict: verb.ReviewerVerdict{
			Skill: ref.skill, Order: i, Accept: true, System: i >= sysStart,
			Reviewed:    pv.Reviewed,
			CarriedFrom: pi + 1,
			Notes:       fmt.Sprintf("carried from attempt %d: quotation unchanged", pi+1),
		}})
	}
	return carried, fresh
}

// gateCarrying runs an edge that declares reviewer_carry. handled is false when
// nothing is carryable, or everything is (carry is moot: every reviewer is
// dispatched) — the caller then runs the edge as it would without the key.
func (g *Engine) gateCarrying(ctx context.Context, item workitem.Item, toStatus string, ordered []reviewerRef, sysStart, parallelCap int) (verb.GateDecision, bool, error) {
	carried, fresh := g.carryable(ctx, item, toStatus, ordered, sysStart)
	idx := make([]int, len(carried))
	for j, c := range carried {
		idx[j] = c.idx
	}
	return carryRound(idx, fresh,
		func(at []int) (verb.GateDecision, error) {
			return g.scheduleGate(ctx, item, toStatus, ordered, at, sysStart, parallelCap)
		},
		func(dec verb.GateDecision) bool { return dec.Gated && !dec.Accept },
		func(dec verb.GateDecision) verb.GateDecision {
			// A reject blocks the edge; the carried accepts are recorded beside it.
			for _, c := range carried {
				g.telemetryEvent(ctx, item.ID, "reviewer", "gate-carried", map[string]any{
					"skill": c.verdict.Skill, "to": toStatus, "carried_from": c.verdict.CarriedFrom,
				})
				dec.Reviewers = append(dec.Reviewers, c.verdict)
			}
			sortReviewers(dec.Reviewers)
			return dec
		},
		mergeGate)
}

// scheduleGate dispatches the reviewers at idx (ascending positions in ordered)
// under the edge's bundle, parallel or serial scheduling, and returns the
// decision with each verdict's Order restored to its position in ordered.
func (g *Engine) scheduleGate(ctx context.Context, item workitem.Item, toStatus string, ordered []reviewerRef, idx []int, sysStart, parallelCap int) (verb.GateDecision, error) {
	sub := make([]reviewerRef, len(idx))
	subSys := 0
	for j, i := range idx {
		sub[j] = ordered[i]
		if i < sysStart {
			subSys++
		}
	}
	dec, err := g.runScheduled(ctx, item, toStatus, sub, subSys, parallelCap)
	for k := range dec.Reviewers {
		if o := dec.Reviewers[k].Order; o >= 0 && o < len(idx) {
			dec.Reviewers[k].Order = idx[o]
		}
	}
	return dec, err
}

func sortReviewers(rs []verb.ReviewerVerdict) {
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].Order < rs[b].Order })
}

// mergeGate folds the confirming pass into the first pass's accepting decision.
// A reject in the confirming pass decides the edge; otherwise the top-level
// fields mirror whichever pass holds the highest-ordered verdict, as an
// all-accept edge mirrors its last.
func mergeGate(first, confirm verb.GateDecision) verb.GateDecision {
	out := first
	if confirm.Gated && (!first.Gated || !confirm.Accept || maxOrder(confirm.Reviewers) > maxOrder(first.Reviewers)) {
		out = confirm
	}
	out.Gated = first.Gated || confirm.Gated
	out.Accept = (!first.Gated || first.Accept) && (!confirm.Gated || confirm.Accept)
	out.Unresolved = append(append([]string(nil), first.Unresolved...), confirm.Unresolved...)
	out.Reviewers = append(append([]verb.ReviewerVerdict(nil), first.Reviewers...), confirm.Reviewers...)
	sortReviewers(out.Reviewers)
	return out
}

func maxOrder(rs []verb.ReviewerVerdict) int {
	m := -1
	for _, r := range rs {
		if r.Order > m {
			m = r.Order
		}
	}
	return m
}
