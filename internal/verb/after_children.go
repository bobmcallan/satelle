package verb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfdot"
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
//
// release is the on-demand selection (`story set --release`). Empty waits on
// every member, as before. ReleaseAll selects every member that has discharged
// the obligation; ids select just those members, each of which must have
// discharged it. A selection that leaves a member out is refused unless the route
// declares a way back into the step from a later one, so the members left out
// stay eligible for a later release. The selection is returned (every member when
// release is empty), nil when the target declares no after_children.
func refuseUndischargedChildren(ctx context.Context, item workitem.Item, toStatus string, release []string) ([]string, error) {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("transition %s→%s refused: %s", item.Status, toStatus, fmt.Sprintf(format, args...))
	}
	obligation := ""
	var spec wfdot.Spec
	if item.ID != "" && item.Kind == workitem.KindStory {
		if s, _, _, ok := governingSpec(ctx, item); ok {
			spec, obligation = s, s.AfterChildren(toStatus)
		}
	}
	if obligation == "" {
		if len(release) > 0 {
			return nil, refuse("--release selects children, and step %q does not wait on children (it declares no after_children)", toStatus)
		}
		return nil, nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	members, err := epicset.Members(ctx, store, item)
	if err != nil {
		if errors.Is(err, epicset.ErrUndetermined) {
			return nil, refuse("%v — the step waits on children and its set cannot be fixed", err)
		}
		return nil, refuse("cannot re-read the children: %v", err)
	}
	idx, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil, refuse("cannot read the workflows to judge the children: %v", err)
	}
	var waiting, all, approved []string
	state := map[string]string{} // member id -> why it waits; "" when discharged
	for _, c := range members {
		all = append(all, c.ID)
		discharged, known := wfgovern.ChildDischarged(wfs, c, obligation)
		switch {
		case discharged:
			approved = append(approved, c.ID)
			state[c.ID] = ""
			continue
		case known:
			state[c.ID] = fmt.Sprintf("%s (%s)", c.ID, c.Status)
		default:
			state[c.ID] = fmt.Sprintf("%s (%s; its route has no step providing %q)", c.ID, c.Status, obligation)
		}
		waiting = append(waiting, state[c.ID])
	}
	var selection []string
	switch {
	case len(release) == 0:
		if len(waiting) > 0 {
			return nil, refuse("children have not discharged %q — done, cancelled, or at or past the step providing it: %s",
				obligation, strings.Join(waiting, ", "))
		}
		return all, nil
	case len(release) == 1 && release[0] == ReleaseAll:
		if len(approved) == 0 {
			return nil, refuse("--release %s found no member that has discharged %q — nothing to release: %s",
				ReleaseAll, obligation, strings.Join(waiting, ", "))
		}
		selection = approved
	default:
		seen := map[string]bool{}
		for _, id := range release {
			if id == ReleaseAll {
				return nil, refuse("--release takes %q or a list of member ids, not both", ReleaseAll)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			why, member := state[id]
			switch {
			case !member:
				return nil, refuse("--release names %s, which is not a member of this container's set", id)
			case why != "":
				return nil, refuse("--release names %s, which has not discharged %q — done, cancelled, or at or past the step providing it", why, obligation)
			}
			selection = append(selection, id)
		}
	}
	if len(selection) < len(all) && !hasWayBackInto(spec, toStatus) {
		return nil, refuse("the release leaves members out and the route declares no way back into %s for a later release — declare a recover edge into %s from a later step, or release every member",
			toStatus, toStatus)
	}
	return selection, nil
}

// ReleaseAll is the `--release` value that selects every member that has
// discharged the step's after_children obligation.
const ReleaseAll = "all"

// hasWayBackInto reports whether the route declares an edge into status from a
// step after it, so a container that leaves status can enter it again.
func hasWayBackInto(spec wfdot.Spec, status string) bool {
	at := spec.SpineIndex(status)
	if at < 0 {
		return false
	}
	for _, tr := range spec.Transitions {
		if tr.To == status && spec.SpineIndex(tr.From) > at {
			return true
		}
	}
	return false
}

// releaseDoc is the container attachment that names the members an entry into an
// after_children step releases; the step's performer merges those and no other.
const releaseDoc = "release"

// recordReleaseSelection writes the selection as the container's release
// document, replacing any earlier one, so each entry names its own members.
// Where no attachment store is configured there is no earlier document to go
// stale, so a full entry proceeds; a requested selection still refuses, since
// the performer could not learn it.
func recordReleaseSelection(ctx context.Context, item workitem.Item, toStatus string, selection []string, requested bool, now time.Time) error {
	if len(selection) == 0 || (!requested && attachmentDir(item) == "") {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The members this entry into %s releases. Merge these and no other:\n\n", toStatus)
	for _, id := range selection {
		fmt.Fprintf(&b, "- %s\n", id)
	}
	if _, _, err := writeAttachedDoc(ctx, item, releaseDoc, releaseDoc, b.String(), now); err != nil {
		return fmt.Errorf("transition %s→%s refused: cannot record the release selection: %w", item.Status, toStatus, err)
	}
	return nil
}
