package verb

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// acquireEngagementLease claims the engagement seat for item at targetStatus
// BEFORE gate+dispatch (sty_8426b9c0). The DOT shape still decides whether the
// target is engaging (config-over-code); the lease records who holds the seat.
//
// Returns:
//   - acquiredThisCall: true when this call newly inserted (or stole) the lease —
//     release-on-abort must free only those.
//   - alreadyInFlight: same owner already holds this item at targetStatus — the
//     transition is a no-op (AC3: never two concurrent dispatches for one id).
//     Sequential plan→in_progress still proceeds (lease.State != target).
//   - err: conflict with another holder, or store failure.
func acquireEngagementLease(ctx context.Context, item workitem.Item, targetStatus string) (acquiredThisCall, alreadyInFlight bool, err error) {
	engaging, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if !ok {
		// No resolving workflow: cannot apply lease rule (same fail-open as the
		// pre-lease single-story scan for incomplete harnesses).
		return false, false, nil
	}
	if !engaging {
		return false, false, nil
	}
	ls, err := requireLease()
	if err != nil {
		// Lease store not wired (tests that only need status): fall back to the
		// derived status scan so existing tests keep working until they wire leases.
		return false, false, refuseSecondEngagingStoryDerived(ctx, item.ID, targetStatus, item)
	}
	owner := lease.ResolveOwner()
	// Stories always occupy the single story seat (one performing story).
	// Tasks get a lease for gate "any engaged" but never the story seat.
	occupiesSeat := item.Kind == workitem.KindStory
	l, outcome, holder, err := ls.Acquire(ctx, item.ID, string(item.Kind), owner, targetStatus, occupiesSeat)
	if err != nil {
		return false, false, err
	}
	switch outcome {
	case lease.OutcomeAcquired, lease.OutcomeStolen:
		_ = l
		return true, false, nil
	case lease.OutcomeInFlight:
		// Concurrent same-story re-engage: no second dispatch (AC3).
		return false, true, nil
	case lease.OutcomeAlreadyHeld:
		// Settled lease, sequential step (or same-status re-set). If committed
		// item status already equals target, no-op; else continue the transition.
		if item.Status == targetStatus {
			// Clear the in_flight we just set — nothing to run.
			_ = ls.ClearInFlight(ctx, item.ID)
			return false, true, nil
		}
		return false, false, nil
	case lease.OutcomeConflict:
		who := holder.ItemID
		if holder.Owner != "" {
			who = fmt.Sprintf("%s (owner %q, state %q)", holder.ItemID, holder.Owner, holder.State)
		}
		self := item.ID
		if self == "" {
			self = "new story"
		}
		return false, false, seatConflictError(self, targetStatus, who)
	default:
		return false, false, nil
	}
}

// seatConflictError is the single seat-refusal text (sty_7b69954a): LIVE holder →
// stop-request arbitration; STALE seat → operator force-release. One function so
// the two call sites cannot drift. Load-bearing phrases for existing tests:
// "one performing story" and "engagement seat".
func seatConflictError(self, targetStatus, who string) error {
	return fmt.Errorf(
		"satelle: refusing to engage %s (→ %s) while %s already holds the engagement seat — one performing story at a time. Inspect with `satelle story seat`. If the holder is LIVE (another agent working it): request preemption with `satelle story stop-request <holder> --reason \"<why>\"` — the holder is then refused forward moves and parks itself (blocked), which frees the seat. If the holder is your own work: finish it or park it (blocked). If the seat is STALE (no live agent, see the heartbeat age): `satelle story seat release <holder>`. Never cancel a healthy story to free the seat — cancelled is terminal",
		self, targetStatus, who)
}

// releaseEngagementLease frees the seat for itemID if held by the current owner.
// Best-effort: not found is a no-op. Used for release-on-abort of a seat THIS
// call newly claimed (same process / same ResolveOwner).
func releaseEngagementLease(ctx context.Context, itemID string) {
	ls, err := requireLease()
	if err != nil {
		return
	}
	_ = ls.Release(ctx, itemID, lease.ResolveOwner())
}

// clearEngagementInFlight drops the in_flight flag after a sequential step
// aborts (gate reject) so a retry can re-enter the edge.
func clearEngagementInFlight(ctx context.Context, itemID string) {
	ls, err := requireLease()
	if err != nil {
		return
	}
	_ = ls.ClearInFlight(ctx, itemID)
}

// confirmEngagementLease records the committed engaging status after Update.
func confirmEngagementLease(ctx context.Context, itemID, committedState string) {
	ls, err := requireLease()
	if err != nil {
		return
	}
	_ = ls.Confirm(ctx, itemID, committedState)
}

// forceReleaseEngagementLease frees the seat on workflow exit (terminal/park)
// regardless of owner — sequential CLI invocations must free the seat when
// status commits to an exit state (sty_8426b9c0).
func forceReleaseEngagementLease(ctx context.Context, itemID string) {
	ls, err := requireLease()
	if err != nil {
		return
	}
	_ = ls.ForceRelease(ctx, itemID)
}

// stopRequestBlocksForward reports whether a pending stop request should refuse
// a transition to targetStatus. Park/terminal/cancel targets are allowed so the
// owner can exit; forward engaging moves are refused with the stop reason (AC5).
func stopRequestBlocksForward(ctx context.Context, item workitem.Item, targetStatus string) error {
	ls, err := requireLease()
	if err != nil {
		return nil
	}
	l, err := ls.Get(ctx, item.ID)
	if err != nil {
		return nil // no lease → nothing to arbitrate
	}
	if l.StopRequestedBy == "" {
		return nil
	}
	// Allow exit paths: terminal or park (shape-derived).
	if targetIsExitState(ctx, item, targetStatus) {
		return nil
	}
	// Allow non-engaging targets in general (already free seat).
	engaging, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if ok && !engaging {
		return nil
	}
	reason := l.StopReason
	if reason == "" {
		reason = "(no reason given)"
	}
	return fmt.Errorf(
		"satelle: refusing %s → %s — stop requested by %q: %s; owner may only park (blocked) or terminate, not advance",
		item.Status, targetStatus, l.StopRequestedBy, reason)
}

// targetIsExitState reports terminal or park via the governing workflow DOT.
func targetIsExitState(ctx context.Context, item workitem.Item, status string) bool {
	idx, err := requireDocIndex()
	if err != nil {
		return false
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return false
	}
	wf, found := wfgovern.GoverningWorkflow(wfs, item)
	if !found {
		return false
	}
	spec, parsed := wfdot.Parse(wf.Body)
	if !parsed {
		return false
	}
	return spec.IsTerminalState(status) || spec.IsParkState(status)
}

// refuseSecondEngagingStoryDerived is the pre-lease scan of committed statuses —
// kept as a fallback when the lease store is unwired (unit tests that only exercise
// status transitions) and for the create-path provisional check before an id exists.
func refuseSecondEngagingStoryDerived(ctx context.Context, excludeID, targetStatus string, item workitem.Item) error {
	if item.Kind != workitem.KindStory {
		return nil
	}
	targetEngaging, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if !ok || !targetEngaging {
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
				"satelle: refusing to engage %s (→ %s) while %s is already engaged (status %q) — one performing story at a time; finish or park the other (blocked)",
				self, targetStatus, who, other.Status)
		}
	}
	return nil
}

// refuseSecondEngagingStory is the create-path pre-check (no id yet) and a
// legacy entry for tests. When the lease store is wired, a live story_seat
// blocks create-into-engaging even during a peer's gate+dispatch window
// (when the peer's committed status is still non-engaging). Falls back to the
// derived status scan when leases are unwired.
func refuseSecondEngagingStory(ctx context.Context, excludeID, targetStatus string, item workitem.Item) error {
	if item.Kind != workitem.KindStory {
		return nil
	}
	targetEngaging, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if !ok || !targetEngaging {
		return nil
	}
	// Lease seat is the authority when wired (covers the pre-commit window).
	if ls, err := requireLease(); err == nil {
		// Look for any story_seat holder (other than excludeID).
		// Acquire with a probe is wrong (would claim). Query via a throwaway path:
		// try Acquire for a synthetic id is also wrong. Use Get on scan via Any + list.
		// Simplest: attempt Acquire for excludeID if set (continuation); else check
		// conflict by acquiring with a temporary probe — NO, that claims.
		// Direct SQL is package-private. Use: Acquire for a unique probe id then
		// immediately release if we got it; if conflict, refuse.
		if excludeID != "" {
			// Item already exists: real acquire path.
			_, _, aerr := acquireEngagementLease(ctx, item, targetStatus)
			return aerr
		}
		probeID := "__seat_probe__"
		owner := lease.ResolveOwner()
		_, outcome, holder, aerr := ls.Acquire(ctx, probeID, "story", owner, targetStatus, true)
		if aerr != nil {
			return aerr
		}
		switch outcome {
		case lease.OutcomeAcquired, lease.OutcomeStolen:
			// Free the probe immediately — we only wanted the conflict check.
			_ = ls.Release(ctx, probeID, owner)
			// Seat was free (or we stole stale). Derived scan still catches
			// committed-status holders that somehow lack a lease.
			return refuseSecondEngagingStoryDerived(ctx, excludeID, targetStatus, item)
		case lease.OutcomeConflict:
			who := "another story"
			if holder != nil {
				who = fmt.Sprintf("%s (owner %q, state %q)", holder.ItemID, holder.Owner, holder.State)
			}
			return seatConflictError("new story", targetStatus, who)
		case lease.OutcomeAlreadyHeld:
			_ = ls.Release(ctx, probeID, owner)
			return refuseSecondEngagingStoryDerived(ctx, excludeID, targetStatus, item)
		}
	}
	return refuseSecondEngagingStoryDerived(ctx, excludeID, targetStatus, item)
}

// storyStatusIsEngaging reports whether status is a non-terminal engaging state
// of the workflow that governs item (same shape predicate as the edit-gate:
// NonTerminalEngagingStates). ok is false when governance cannot be resolved.
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
