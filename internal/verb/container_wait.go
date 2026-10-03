package verb

import (
	"context"

	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// waitsOnOpenChildrenAt reports whether item entering status is a container
// idling at a step its route declares `waits_on_children` while a child is still
// open (sty_56648ae5). Such a story holds a status but performs nothing, so it
// takes no engagement lease: the seat it would hold is the very tree its
// children must engage from. The rule is wfgovern.WaitsOnOpenChildren — the one
// owner the edit gate's hook also reads — and the wait is the route's
// declaration, never a status or category name here.
//
// Fails open (false, an ordinary seat) when governance, the doc index or the
// store cannot resolve, matching the hook: an unresolved route is never wrongly
// treated as waiting.
func waitsOnOpenChildrenAt(ctx context.Context, item workitem.Item, status string) bool {
	if item.Kind != workitem.KindStory {
		return false
	}
	spec, _, _, ok := governingSpec(ctx, item)
	if !ok || !spec.WaitsOnChildren(status) {
		return false
	}
	idx, err := requireDocIndex()
	if err != nil {
		return false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return false
	}
	store, err := requireWorkItem()
	if err != nil {
		return false
	}
	items, err := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		return false
	}
	return wfgovern.WaitsOnOpenChildren(item, status, spec, items, wfs)
}
