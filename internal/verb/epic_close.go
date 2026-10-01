package verb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// refuseOpenEpicChildren is the commit-time check on an epic-parent's close
// (sty_9f4f8e12): Go re-reads the epic set from the store NOW and refuses while
// any member is neither done nor cancelled. The children list a close-gate
// reviewer was handed is a snapshot taken when that reviewer was prepared — a
// hint, never this check. Fail closed: an epic whose set cannot be fixed (no
// epic:<theme> tag, or two epic-parents on one tag) does not close either.
//
// Mechanism, not a verdict on process: which states are terminal, and what
// resolves a member, stay the workflow's declaration — this only refuses to
// commit a declared terminal status over members the workflow still holds open.
// A non-epic container is unchanged.
func refuseOpenEpicChildren(ctx context.Context, current workitem.Item, toStatus string) error {
	if current.Kind != workitem.KindStory || !epicset.IsEpicParent(current) {
		return nil
	}
	if !targetIsTerminalStateOnly(ctx, current, toStatus) {
		return nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return err
	}
	set, err := epicset.Resolve(ctx, store, current)
	if err != nil {
		if errors.Is(err, epicset.ErrUndetermined) {
			return fmt.Errorf("transition %s→%s refused: %v — the container does not close", current.Status, toStatus, err)
		}
		return fmt.Errorf("transition %s→%s refused: cannot re-read the epic set: %w", current.Status, toStatus, err)
	}
	var wfs []docindex.Doc
	if idx, ierr := requireDocIndex(); ierr == nil {
		wfs, _ = idx.List(ctx, "workflows")
	}
	var open []string
	for _, c := range set.Children {
		if resolved, known := wfgovern.ChildResolved(wfs, c); known && resolved {
			continue
		}
		open = append(open, fmt.Sprintf("epic child %s is %s", c.ID, c.Status))
	}
	if len(open) > 0 {
		return fmt.Errorf("transition %s→%s refused: %s (every member of %s must be done or cancelled; set re-read from the store at the commit)",
			current.Status, toStatus, strings.Join(open, "; "), set.Theme)
	}
	return nil
}
