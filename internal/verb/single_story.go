package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Nothing is held while a story performs (sty_9c782f0e). Engaging a story is a status
// change on its chain: exclusivity between locations is the chain's
// compare-and-append on its base, presence is the working_on row, concurrency
// is checkConcurrency, and a transition running its gates and dispatch is the
// open transition_started row.

// engageCheck runs the admission rules for moving item to targetStatus before
// any gate or dispatch runs, so a refusal changes nothing.
//
// Returns:
//   - engaging: targetStatus is a performing state this move enters (a
//     container idling at a waits_on_children step performs nothing, so it is
//     not engaging there).
//   - alreadyInFlight: an open transition from this host already runs item to
//     targetStatus — the move is a no-op, never a second dispatch for one id.
//   - err: the wave, the epic's readiness or the concurrency limit refuses.
func engageCheck(ctx context.Context, item workitem.Item, targetStatus string) (engaging, alreadyInFlight bool, err error) {
	eng, ok := storyStatusIsEngaging(ctx, item, targetStatus)
	if !ok || !eng {
		return false, false, nil
	}
	if item.ID != "" && waitsOnOpenChildrenAt(ctx, item, targetStatus) {
		return false, false, nil
	}
	// A child of a scheduled epic engages only if the wave returns it
	// (sty_54004fbd) ...
	if err := refuseOffWave(ctx, item, targetStatus); err != nil {
		return false, false, err
	}
	// ... and only once its epic has been readied (sty_a476b913): the epic's
	// round sets the lanes and edges the child's route depends on.
	if err := refuseUnreadiedEpic(ctx, item, item.Status, targetStatus); err != nil {
		return false, false, err
	}
	if err := checkConcurrency(ctx, item, targetStatus); err != nil {
		return false, false, err
	}
	if item.ID != "" && ledgerStore != nil {
		if open, lerr := ledgerStore.ListOpenTransitions(ctx, item.ID, time.Now()); lerr == nil {
			if t, ok := open[item.ID]; ok && t.To == targetStatus && t.Host == ledger.Hostname() && t.Pid != os.Getpid() {
				return true, true, nil
			}
		}
	}
	if sid := config.ResolveSession(); sid != "" {
		_ = os.Setenv(config.SessionEnv, sid)
	}
	return true, false, nil
}

// openStarts remembers, per item, the transition_started row this process
// wrote, so activity rows name it without a ledger read.
var openStarts sync.Map

// beginTransition records that item's move from → to has begun its gates and
// dispatch, and returns the row id endTransition closes. "" without a ledger. A
// failed append is returned: a transition nobody can see in flight must not run.
func beginTransition(ctx context.Context, item workitem.Item, from, to string) (string, error) {
	if ledgerStore == nil || item.ID == "" {
		return "", nil
	}
	p := ledger.TransitionStartedPayload{
		From: from, To: to, Pid: os.Getpid(), Host: ledger.Hostname(),
		Attempt: parkAttemptFrom(ctx), Session: config.ResolveSession(),
	}
	payload, _ := json.Marshal(p)
	e, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: item.ID, Kind: ledger.KindTransitionStarted, Actor: "executor",
		Body: fmt.Sprintf("transition %s → %s started", from, to), Payload: payload,
	}, time.Now())
	if err != nil {
		return "", fmt.Errorf("satelle: cannot record the start of %s → %s for %s: %w", from, to, item.ID, err)
	}
	openStarts.Store(item.ID, e.ID)
	return e.ID, nil
}

// endTransition closes the transition_started row startID with outcome
// (committed, aborted). Best-effort; a no-op for an empty id.
func endTransition(ctx context.Context, itemID, startID, outcome string) {
	if ledgerStore == nil || startID == "" {
		return
	}
	openStarts.CompareAndDelete(itemID, startID)
	payload, _ := json.Marshal(ledger.TransitionEndedPayload{Start: startID, Outcome: outcome})
	_, _ = ledgerStore.Append(context.WithoutCancel(ctx), ledger.AppendInput{
		StoryID: itemID, Kind: ledger.KindTransitionEnded, Actor: "executor",
		Body: "transition " + outcome, Payload: payload,
	}, time.Now())
}

// RecordTransitionActivity records the phase or dispatch the open transition
// this process began on itemID is running. A row is written only when the phase
// or the agent changes, never per event. Best-effort: observability must not
// fail a transition.
func RecordTransitionActivity(ctx context.Context, itemID string, p ledger.TransitionActivityPayload) {
	v, ok := openStarts.Load(itemID)
	if !ok || ledgerStore == nil {
		return
	}
	p.Start = v.(string)
	key := p.Start + "\x00" + p.Label + "\x00" + p.Agent + "\x00" + p.Model
	if _, dup := activitySeen.LoadOrStore(key, true); dup {
		return
	}
	body := "activity " + p.Label
	if p.Agent != "" {
		body = "dispatch " + p.Agent
	}
	payload, _ := json.Marshal(p)
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: itemID, Kind: ledger.KindTransitionActivity, Actor: "executor",
		Body: body, Payload: payload,
	}, time.Now())
}

// activitySeen dedupes RecordTransitionActivity within this process.
var activitySeen sync.Map

// stopRequestBlocksForward reports whether a pending stop request should refuse
// a transition to targetStatus. Park/terminal/cancel targets are allowed so the
// driver can exit; forward engaging moves are refused with the stop reason. A
// request is pending from its stop_requested row until a later transition takes
// the story to an at-rest state.
func stopRequestBlocksForward(ctx context.Context, item workitem.Item, targetStatus string) error {
	req, ok := pendingStopRequest(ctx, item)
	if !ok {
		return nil
	}
	if targetIsExitState(ctx, item, targetStatus) {
		return nil
	}
	engaging, eok := storyStatusIsEngaging(ctx, item, targetStatus)
	if eok && !engaging {
		return nil
	}
	reason := req.Reason
	if reason == "" {
		reason = "(no reason given)"
	}
	return fmt.Errorf(
		"satelle: refusing %s → %s — stop requested by %q: %s; the driver may only park (blocked) or terminate, not advance",
		item.Status, targetStatus, req.By, reason)
}

// pendingStopRequest is the latest stop request on item that no later move to
// an at-rest state has answered.
func pendingStopRequest(ctx context.Context, item workitem.Item) (ledger.StopRequestedPayload, bool) {
	if ledgerStore == nil || item.ID == "" {
		return ledger.StopRequestedPayload{}, false
	}
	reqs, _, err := ledgerStore.ListNewest(ctx, ledger.ListFilter{StoryID: item.ID, Kind: ledger.KindStopRequested, Limit: 1})
	if err != nil || len(reqs) == 0 {
		return ledger.StopRequestedPayload{}, false
	}
	last := reqs[len(reqs)-1]
	moves, _, err := ledgerStore.ListNewest(ctx, ledger.ListFilter{StoryID: item.ID, Kind: ledger.KindStatusTransition, Limit: 50})
	if err == nil {
		for _, m := range moves {
			if m.CreatedAt.After(last.CreatedAt) && targetIsExitState(ctx, item, TransitionTo(m)) {
				return ledger.StopRequestedPayload{}, false
			}
		}
	}
	var p ledger.StopRequestedPayload
	_ = json.Unmarshal(last.Payload, &p)
	return p, true
}

// maxDirtyClosePaths caps the paths a refused close names.
const maxDirtyClosePaths = 20

// refuseDirtyClose refuses a transition to a terminal state while the working
// tree this story was engaged from at this location holds uncommitted gated
// changes. A close is a terminal state or a cancel exit (a park that is not the
// resuming park). The close ends the engagement, and with it the permission to
// commit, so the operator must be told before it is gone. It is mechanism, not a
// workflow gate: it applies to any workflow, and it runs before any gate or
// performer is dispatched.
//
// The tree is read from the story's own latest working_on row at this location
// only, so a story engaged from nowhere here names nothing. The resuming park
// (blocked) is not a close, and every uncertainty (no probe, no row, no recorded
// tree, probe error) fails open.
func refuseDirtyClose(ctx context.Context, item workitem.Item, target string) error {
	if dirtyPathsProbe == nil || !EndsEngagementForGood(ctx, item, target) {
		return nil
	}
	w, ok := LatestLocalWorkingOn(ctx, item.ID)
	if !ok || w.Worktree == "" {
		return nil
	}
	paths, err := dirtyPathsProbe(ctx, w.Worktree)
	if err != nil || len(paths) == 0 {
		return nil
	}
	shown, more := paths, ""
	if len(shown) > maxDirtyClosePaths {
		more = fmt.Sprintf("\n  +%d more", len(shown)-maxDirtyClosePaths)
		shown = shown[:maxDirtyClosePaths]
	}
	return fmt.Errorf(
		"satelle: refusing %s → %s — working tree %s has uncommitted changes outside the edit-exempt paths:\n  %s%s\n"+
			"commit or discard them before closing — closing ends the engagement that permits the commit; then re-run: satelle story set %s --status %s",
		item.Status, target, w.Worktree, strings.Join(shown, "\n  "), more, item.ID, target)
}

// refuseReopen refuses any move out of a terminal state, even along an edge the
// route declares: a closed story's record is history, and new work on it is a
// new story carrying a back-reference tag. Terminal is the governing workflow's
// own classification (targetIsTerminalStateOnly), so a park such as the cancel
// sink is not covered.
func refuseReopen(ctx context.Context, item workitem.Item, target string) error {
	if target == item.Status || !targetIsTerminalStateOnly(ctx, item, item.Status) {
		return nil
	}
	return fmt.Errorf(
		"satelle: refusing to reopen %s (%s → %s) — %s is terminal and its record is closed history; raise a new story carrying a back-reference tag (e.g. follows:%s) instead",
		item.ID, item.Status, target, item.Status, item.ID)
}

// targetIsExitState reports terminal, park or wait via the governing workflow:
// every state at which the story stops holding its engagement.
func targetIsExitState(ctx context.Context, item workitem.Item, status string) bool {
	spec, _, _, ok := governingSpec(ctx, item)
	if !ok {
		return false
	}
	return spec.IsAtRest(status)
}

// targetIsWaitState reports whether status is a wait step of item's workflow.
func targetIsWaitState(ctx context.Context, item workitem.Item, status string) bool {
	spec, _, _, ok := governingSpec(ctx, item)
	return ok && spec.IsWaitState(status)
}

// targetIsTerminalStateOnly reports terminal (never park) via the governing
// workflow's costview.Clock — the story clock's end predicate (sty_8eae81ac
// A1): a park (blocked) leaves the story open, so it must not stop the clock
// the way targetIsExitState's broader terminal-or-park test would. A thin
// wrapper over wfgovern.ClockFor (sty_b8542a3a) so this classification has one
// owner, shared with storyStatusIsEngaging and costview's own accumulation.
func targetIsTerminalStateOnly(ctx context.Context, item workitem.Item, status string) bool {
	clk, ok := clockFor(ctx, item)
	if !ok {
		return false
	}
	return clk.Terminal(status)
}

// EndsEngagementForGood reports an exit the story does not resume from: a
// terminal state, or a park state that is not the resuming park (the cancel
// sink, which the route synthesises without a terminal shape). Both end the
// engagement via targetIsExitState; only the resuming park (blocked) keeps the
// way back to the same tree.
func EndsEngagementForGood(ctx context.Context, item workitem.Item, status string) bool {
	if targetIsTerminalStateOnly(ctx, item, status) {
		return true
	}
	spec, _, _, ok := governingSpec(ctx, item)
	return ok && spec.IsParkState(status) && !spec.IsResumePark(status)
}

// statusIsParkState reports whether status is a park state of item's workflow.
// Shape-derived, so re-anchoring keys to the route rather than a status literal.
func statusIsParkState(ctx context.Context, item workitem.Item, status string) bool {
	spec, _, _, ok := governingSpec(ctx, item)
	if !ok {
		return false
	}
	return spec.IsParkState(status)
}

// storyStatusIsEngaging reports whether status is a non-terminal engaging state
// of the workflow that governs item (same shape predicate as the edit-gate:
// NonTerminalEngagingStates). ok is false when governance cannot be resolved.
// A thin wrapper over wfgovern.ClockFor (sty_b8542a3a) so this classification
// has one owner, shared with targetIsTerminalStateOnly and costview's own
// accumulation.
func storyStatusIsEngaging(ctx context.Context, item workitem.Item, status string) (engaging bool, ok bool) {
	clk, ok := clockFor(ctx, item)
	if !ok {
		return false, false
	}
	return clk.Engaging(status), true
}
