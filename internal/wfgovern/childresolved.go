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
