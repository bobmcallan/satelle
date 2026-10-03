package verb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// refuseUndischargedChildren refuses a container's transition into a step its
// route declares `after_children = "<obligation>"` while any member has not
// discharged that obligation on its own route. The refusal names each member
// still holding the step back, so the driver knows whom to drive.
//
// This is entry arbitration only. The step it guards may name a performer, and
// that performer acquires a seat through the ordinary engaging rule; nothing here
// runs git, merges, or decides what the performer does. Which obligation to wait
// for is the route's declaration, and "discharged" is wfgovern.ChildDischarged —
// never a status or category name here.
//
// Once the target is known to declare the wait, every failure to answer (the
// store, the doc index, an epic set that cannot be fixed) refuses: unlike the
// wave check, a guess here would let a merge start over unfinished children, so
// this fails closed, like refuseOpenEpicChildren. A target with no after_children
// is a no-op, which leaves container engagement exactly as it was.
func refuseUndischargedChildren(ctx context.Context, item workitem.Item, toStatus string) error {
	if item.ID == "" || item.Kind != workitem.KindStory {
		return nil
	}
	spec, _, _, ok := governingSpec(ctx, item)
	if !ok {
		return nil
	}
	obligation := spec.AfterChildren(toStatus)
	if obligation == "" {
		return nil
	}
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("transition %s→%s refused: %s", item.Status, toStatus, fmt.Sprintf(format, args...))
	}
	store, err := requireWorkItem()
	if err != nil {
		return err
	}
	members, err := epicset.Members(ctx, store, item)
	if err != nil {
		if errors.Is(err, epicset.ErrUndetermined) {
			return refuse("%v — the step waits on children and its set cannot be fixed", err)
		}
		return refuse("cannot re-read the children: %v", err)
	}
	idx, err := requireDocIndex()
	if err != nil {
		return err
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return refuse("cannot read the workflows to judge the children: %v", err)
	}
	var waiting []string
	for _, c := range members {
		discharged, known := wfgovern.ChildDischarged(wfs, c, obligation)
		switch {
		case discharged:
		case known:
			waiting = append(waiting, fmt.Sprintf("%s (%s)", c.ID, c.Status))
		default:
			waiting = append(waiting, fmt.Sprintf("%s (%s; its route has no step providing %q)", c.ID, c.Status, obligation))
		}
	}
	if len(waiting) > 0 {
		return refuse("children have not discharged %q — done, cancelled, or at or past the step providing it: %s",
			obligation, strings.Join(waiting, ", "))
	}
	return nil
}
