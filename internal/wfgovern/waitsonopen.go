package wfgovern

import (
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WaitsOnOpenChildren reports whether container is idling at a step its route
// declares `waits_on_children` while at least one member is still open
// (sty_7f3e6fd3). Such a story holds a status but performs nothing, so neither
// the edit gate nor the engagement lease treats it as a performing seat — one
// owner for the rule, shared by the hook and the verb layer (sty_56648ae5).
//
// A child is resolved by ChildResolved; a child whose workflow cannot be
// resolved does not keep the container waiting, so the container then counts as
// performing. Which step waits is the route's declaration; no status or
// category name appears here.
func WaitsOnOpenChildren(container workitem.Item, status string, spec wfdot.Spec, items []workitem.Item, workflows []docindex.Doc) bool {
	if !spec.WaitsOnChildren(status) {
		return false
	}
	// Membership is epicset's: an epic-parent's members are its tag set, any
	// other container's are its parent_id links (sty_9f4f8e12). An epic whose
	// set cannot be fixed holds no members here — the close itself refuses it.
	members, _ := epicset.MembersFromItems(items, container)
	for _, c := range members {
		if resolved, known := ChildResolved(workflows, c); !known || resolved {
			continue
		}
		return true
	}
	return false
}
