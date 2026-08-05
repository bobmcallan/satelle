package verb

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// refuseSkippedStep refuses a status change that is not a declared DOT edge of
// the item's governing workflow (sty_ebd3d666). Agents must walk declared edges
// (including recovery/park/cancel when the graph names them) — skipping a
// required step (e.g. backlog→in_progress when only backlog→plan exists) is a
// hard refuse naming the expected successor(s).
//
// Park resume (sty_f75286dc): when ParkOrigin is set, leaving a park node is
// allowed only to that origin (resume) or via an explicit non-performing exit
// edge (e.g. cancelled). Resume-to-origin is not an N×2 edge explosion — it is
// enforced from the work_items.park_origin column.
//
// Fail-open when the governing workflow cannot be resolved or has no DOT: those
// deployments are owned by structure/engage checks, not this fence. Stories and
// tasks with a resolvable DOT are fenced; executions follow their own paths.
func refuseSkippedStep(ctx context.Context, current workitem.Item, toStatus string) error {
	if current.Kind != workitem.KindStory && current.Kind != workitem.KindTask {
		return nil
	}
	from := current.Status
	if from == toStatus {
		return nil
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil
	}
	spec, wfName, _, serr := wfgovern.SpecFor(wfs, current)
	ok := serr == nil
	if !ok {
		return nil
	}

	// Park resume-to-origin (sty_f75286dc): origin is authoritative state.
	if spec.IsParkState(from) && strings.TrimSpace(current.ParkOrigin) != "" {
		origin := current.ParkOrigin
		if toStatus == origin {
			return nil // resume — always legal
		}
		// Explicit non-performing exits (cancelled, etc.) still use declared edges.
		if spec.HasEdge(from, toStatus) && !spec.IsPerformingState(toStatus) {
			return nil
		}
		return wfgovern.Refusal{
			Rule: wfgovern.RuleParkResume, Item: current.ID, Workflow: wfName,
			From: from, To: toStatus,
			Why: fmt.Sprintf(
				"a parked story resumes to the state it parked from (%q), so the gates already passed to reach it are not re-run and none are skipped",
				origin),
			Alternatives: append([]string{origin}, declaredExits(spec, from)...),
			Remedy:       "cancelled and other declared non-performing exits remain open",
		}
	}

	if spec.HasEdge(from, toStatus) {
		return nil
	}
	// Unknown from (legacy status not in graph): still refuse if to is not a
	// free-form — name successors of from when any, else generic.
	next := spec.Successors(from)
	if len(next) == 0 {
		// Route drift is the specific cause worth naming (sty_6e4f7fd8): the story
		// sits on a state the lane its CATEGORY derives now does not declare, so
		// the lane changed under work already in flight. The generic text below
		// sends the reader to fix a workflow that is not broken. Structural, not a
		// verdict on the drift — a status off the lane has no legal edge at all.
		if d, drifted := wfgovern.DetectRouteDrift(current, spec, RouteWalked(current)); drifted && !d.StatusOnRoute {
			return wfgovern.Refusal{
				Rule: wfgovern.RuleRouteDrift, Item: current.ID, Workflow: wfName,
				From: from, To: toStatus,
				Why:    wfgovern.DriftWhy(d),
				Remedy: wfgovern.DriftRemedy(d, spec.Start()),
			}
		}
		return wfgovern.Refusal{
			Rule: wfgovern.RuleSkippedStep, Item: current.ID, Workflow: wfName,
			From: from, To: toStatus,
			Why:    fmt.Sprintf("the route declares no step at all after %s, so there is nothing to move to from here", from),
			Remedy: "open a session on a legal path, or fix the workflow's declaration of done",
		}
	}
	return wfgovern.Refusal{
		Rule: wfgovern.RuleSkippedStep, Item: current.ID, Workflow: wfName,
		From: from, To: toStatus,
		Why: fmt.Sprintf(
			"the route puts %s after %s; a step may not be skipped because its gates are the only place the work is judged",
			strings.Join(next, " or "), from),
		Alternatives: next,
	}
}

// declaredExits returns the declared NON-PERFORMING targets of from — the exits a
// parked story may still take (cancel and the like), so a park-resume refusal
// names every legal move rather than only the origin.
func declaredExits(spec wfdot.Spec, from string) []string {
	var out []string
	for _, to := range spec.Successors(from) {
		if !spec.IsPerformingState(to) {
			out = append(out, to)
		}
	}
	return out
}

// parkOriginForTransition returns the park_origin value to stamp on a status
// transition (sty_f75286dc). Entering a park node stores the prior status;
// leaving a park node clears it. Nil means leave the column unchanged.
func parkOriginForTransition(ctx context.Context, current workitem.Item, toStatus string) *string {
	idx, err := requireDocIndex()
	if err != nil {
		return nil
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil
	}
	spec, _, _, serr := wfgovern.SpecFor(wfs, current)
	ok := serr == nil
	if !ok {
		return nil
	}
	empty := ""
	if spec.IsParkState(toStatus) && !spec.IsParkState(current.Status) {
		// Enter park: origin is the prior performing (or other) status.
		o := current.Status
		return &o
	}
	if spec.IsParkState(current.Status) && !spec.IsParkState(toStatus) {
		// Leave park (resume or cancel): clear origin.
		return &empty
	}
	return nil
}
