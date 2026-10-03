package wfgovern

import (
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// ChildResolved reports whether a container's member no longer holds the
// container open: it sits at a terminal state of its own governing workflow, or
// at a park state that does not resume (cancelled). A resuming park (blocked) is
// still open work. known is false when no route resolves for the member, so the
// caller chooses fail-open (a hint) or fail-closed (a commit guard). One owner
// for the predicate, shared by the close guard and the engage-wait hook.
func ChildResolved(workflows []docindex.Doc, child workitem.Item) (resolved, known bool) {
	spec, _, _, err := SpecFor(workflows, child)
	if err != nil {
		return false, false
	}
	return spec.IsTerminalState(child.Status) || (spec.IsParkState(child.Status) && !spec.IsResumePark(child.Status)), true
}

// ChildDischarged reports whether a container's member has discharged
// obligation on its own route: it is resolved (ChildResolved — a cancelled
// member never holds the container), or it sits at or past the route step that
// provides the obligation. Entry to that step was gated, so being at or past it
// is the proof; route order is the Spec's derived order, never a status name.
//
// known is false when the member's route cannot be resolved, or has no step
// providing obligation and the member is not resolved. Either way the caller
// treats the member as not discharged; known only lets it say why. A member
// holding a status off the spine (a resuming park) has not discharged.
func ChildDischarged(workflows []docindex.Doc, child workitem.Item, obligation string) (discharged, known bool) {
	if resolved, ok := ChildResolved(workflows, child); !ok {
		return false, false
	} else if resolved {
		return true, true
	}
	spec, _, _, err := SpecFor(workflows, child)
	if err != nil {
		return false, false
	}
	step, ok := spec.ObligationStep(obligation)
	if !ok {
		return false, false
	}
	at, want := spec.SpineIndex(child.Status), spec.SpineIndex(step.Name)
	return at >= 0 && at >= want, true
}
