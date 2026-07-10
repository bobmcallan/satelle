package verb

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// allowParallelStories is the [gate] allow_parallel opt-out. Default false =
// enforce one non-terminal engaging story at a time (sty_c7149f8a). true only
// disables the blocker; it does not implement parallel worktrees/merge.
var allowParallelStories bool

// SetAllowParallelStories wires the opt-out from satelle.toml [gate] allow_parallel
// at app bootstrap. Pass false (default) to enforce.
func SetAllowParallelStories(v bool) { allowParallelStories = v }

// refuseSecondEngagingStory refuses when targetStatus would leave a second story
// in a non-terminal engaging state of its governing workflow. excludeID is the
// story being created/moved (empty before create assigns an id). Parked
// (agent=reviewer, e.g. blocked) and terminal/start states never occupy a slot.
// Stories only — tasks/executions are out of scope for this process rule.
func refuseSecondEngagingStory(ctx context.Context, excludeID, targetStatus string, item workitem.Item) error {
	if allowParallelStories {
		return nil
	}
	if item.Kind != workitem.KindStory {
		return nil
	}
	targetEngaging, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if !ok {
		// No resolving workflow / non-DOT: cannot apply the rule. Production
		// repos after init always have workflows; incomplete test harnesses and
		// pre-init stubs keep creating backlog without this gate.
		return nil
	}
	if !targetEngaging {
		return nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return err
	}
	others, err := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		return fmt.Errorf("satelle: cannot enforce single-story rule (list failed: %w)", err)
	}
	for _, other := range others {
		if other.ID == excludeID {
			continue
		}
		engaged, otherOK := storyStatusIsEngaging(ctx, other, other.Status)
		if !otherOK {
			// Unresolvable peer: do not treat as occupying a seat.
			continue
		}
		if engaged {
			who := other.ID
			if other.Title != "" {
				who = other.ID + ` "` + other.Title + `"`
			}
			self := excludeID
			if self == "" {
				self = "new story"
			}
			return fmt.Errorf(
				"satelle: refusing to engage %s (→ %s) while %s is already engaged (status %q) — one performing story at a time; finish or park the other (blocked), or set [gate] allow_parallel = true to opt out (opt-out only disables this blocker — parallel still needs workflow/worktree design)",
				self, targetStatus, who, other.Status)
		}
	}
	return nil
}

// storyStatusIsEngaging reports whether status is a non-terminal engaging state
// of the workflow that governs item (same predicate as the edit-gate engagement
// check: NonTerminalEngagingStates). ok is false when governance cannot be
// resolved (no workflow / non-DOT / index unwired).
func storyStatusIsEngaging(ctx context.Context, item workitem.Item, status string) (engaging bool, ok bool) {
	idx, err := requireDocIndex()
	if err != nil {
		return false, false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return false, false
	}
	wf, found := wfgovern.GoverningWorkflow(wfs, item)
	if !found {
		return false, false
	}
	spec, parsed := wfdot.Parse(wf.Body)
	if !parsed {
		return false, false
	}
	for _, s := range spec.NonTerminalEngagingStates() {
		if s == status {
			return true, true
		}
	}
	return false, true
}
