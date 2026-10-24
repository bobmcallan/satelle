package verb

import (
	"context"
	"errors"
	"fmt"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// refuseOffWave refuses a NEW engagement of a child that the wave of its
// scheduled epic would not return (sty_54004fbd). The wave is the single answer
// to "who may start": story wave reports it, this enforces it, both through
// assessWave, so they cannot disagree. It reads the store and the workflow index
// and writes nothing, so a refusal takes no seat and leaves no ledger row.
//
// Everything that is not an unambiguous off-wave child passes (nil):
//   - a story with no epic container, or one that is itself an epic-parent;
//   - a container whose route declares two schedules its status selects neither
//     of — that refusal is a reporting one, not an engagement one, or such an
//     epic would lock. A container declaring none is parallel by default, so it
//     is enforced like any scheduled epic;
//   - an epic whose set is undetermined, or any store/route lookup that fails
//     (fail open, like waitsOnOpenChildrenAt: an unresolved route never blocks);
//   - a child already mid-flight (its current status engaging): the wave keeps
//     an engaged child non-terminal, so re-checking every later transition would
//     refuse the very story the wave let in.
//
// Seat mode and the one-engagement-per-tree rule are not decided here; they run
// afterwards in acquireEngagementLease, so a wave-eligible child still meets
// lease.ErrTreeConflict in a worktree that is already leased.
func refuseOffWave(ctx context.Context, item workitem.Item, targetStatus string) error {
	if item.ID == "" || item.Kind != workitem.KindStory || epicset.IsEpicParent(item) {
		return nil
	}
	if midFlight, ok := storyStatusIsEngaging(ctx, item, item.Status); !ok || midFlight {
		return nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil
	}
	containers := waveContainersOf(ctx, store, item)
	if len(containers) == 0 {
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
	for _, container := range containers {
		if reason, off := offWaveReason(ctx, store, wfs, container, item); off {
			return offWaveError(item.ID, targetStatus, container.ID, reason)
		}
	}
	return nil
}

// refuseUnreadiedEpic refuses the first move of an epic child out of its route's
// entry state into an engaging status while the epic-parent that owns its set has
// not passed its readiness round (sty_a476b913). The epic's round is what
// sequences its children — depends-on edges, lane tags — so engaging a child
// before it ran leaves them unset. It keys on the child's entry state, not on a
// readiness step of the child, so a child on a route with no propose step (a
// substrate or docs route) is covered too.
//
// fromStatus is the child's status before the move: the stored status on a
// transition, the route's entry state for a create. Passes (nil):
//   - anything but a story, an epic-parent itself, or a story in no epic set;
//   - a move that does not leave the entry state, or whose target is not
//     engaging, or is a cancel or terminal exit — abandoning a child of an
//     un-readied epic stays open;
//   - an epic whose route declares no readiness step, and any store, index or
//     route lookup that fails (fail open, like refuseOffWave).
//
// It reads the ledger and writes nothing, so a refusal takes no seat.
func refuseUnreadiedEpic(ctx context.Context, item workitem.Item, fromStatus, targetStatus string) error {
	if item.Kind != workitem.KindStory || epicset.IsEpicParent(item) {
		return nil
	}
	entry, ok := storyEntryState(ctx, item)
	if !ok || fromStatus != entry || targetStatus == entry {
		return nil
	}
	if engaging, ok := storyStatusIsEngaging(ctx, item, targetStatus); !ok || !engaging || isCancelOrTerminalExit(ctx, item, targetStatus) {
		return nil
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil
	}
	for _, container := range waveContainersOf(ctx, store, item) {
		edge, err := readinessEdgeFor(ctx, container)
		if err != nil {
			continue
		}
		if _, holds := readinessHolds(ctx, container, edge); holds {
			continue
		}
		subject := item.ID
		if subject == "" {
			subject = "new story"
		}
		return fmt.Errorf("satelle: refusing to move %s out of %q (-> %q) — its epic %s has not been readied (no accepted readiness round%s): run satelle story ready %s first, which sequences the epic's children",
			subject, fromStatus, targetStatus, container.ID, markClause(edge), container.ID)
	}
	return nil
}

// markClause names the readiness mark an un-readied epic would carry, for the
// refusal text; empty when the route declares no mark.
func markClause(edge readinessEdge) string {
	if edge.Step.Mark == "" {
		return ""
	}
	return fmt.Sprintf("; it would carry %q", edge.Step.Mark)
}

// offWaveReason reports whether container's wave leaves item out, and why — the
// wave's own words (an omitted child's reason, the sequential refusal), or the
// set-membership gap for a parent_id-only child.
func offWaveReason(ctx context.Context, store *workitem.Store, wfs []docindex.Doc, container, item workitem.Item) (string, bool) {
	res, err := assessWave(ctx, store, wfs, container)
	var tooWide errWaveTooWide
	if err != nil && !errors.As(err, &tooWide) {
		return "", false // no usable schedule, undetermined set or lookup failure
	}
	for _, id := range res.Runnable {
		if id == item.ID {
			return "", false
		}
	}
	for _, o := range res.Omitted {
		if o.ID == item.ID {
			return o.Reason, true
		}
	}
	if err != nil {
		return err.Error(), true
	}
	if !inEpicSet(ctx, store, container, item) {
		return fmt.Sprintf("%s is not in the epic set of %s (it carries no %s tag)", item.ID, container.ID, setThemeOf(container)), true
	}
	return "", false
}

// waveContainersOf finds the epic-parent containers item is a child of: the
// epic-parent its parent_id names, and the parent of each epic:<theme> it
// carries. De-duplicated, in a stable order.
func waveContainersOf(ctx context.Context, store *workitem.Store, item workitem.Item) []workitem.Item {
	var out []workitem.Item
	seen := map[string]bool{}
	add := func(c workitem.Item) {
		if !seen[c.ID] && c.Kind == workitem.KindStory && epicset.IsEpicParent(c) {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	if item.ParentID != "" {
		if p, err := store.Get(ctx, item.ParentID); err == nil {
			add(p)
		}
	}
	for _, theme := range epicset.Themes(item) {
		if p, found, err := epicset.ParentOf(ctx, store, theme); err == nil && found {
			add(p)
		}
	}
	return out
}

// inEpicSet reports whether item is a member of container's epic set.
func inEpicSet(ctx context.Context, store *workitem.Store, container, item workitem.Item) bool {
	set, err := epicset.Resolve(ctx, store, container)
	if err != nil {
		return true // cannot tell: do not refuse on a guess
	}
	for _, c := range set.Children {
		if c.ID == item.ID {
			return true
		}
	}
	return false
}

// setThemeOf names the epic:<theme> tag that defines container's set, for the
// refusal text.
func setThemeOf(container workitem.Item) string {
	if themes := epicset.Themes(container); len(themes) > 0 {
		return themes[0]
	}
	return epicset.TagPrefix + "<theme>"
}

func offWaveError(self, targetStatus, container, reason string) error {
	return fmt.Errorf("satelle: refusing to engage %s (→ %s) — it is not in the wave of epic %s: %s. `satelle story wave %s` lists who may start",
		self, targetStatus, container, reason, container)
}
