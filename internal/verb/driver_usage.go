package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// driverSnapshotter is swappable in tests (sty_81caa41b) so a test can drive
// the recorder against a fixture-backed agentcli.DriverSnapshot without a real
// session record on disk.
//
// The default also hands the reader the tree the session published as its home
// (config.PublishedSessionRoot): a session started in the main tree that drives a
// story in a worktree keeps its record under the main tree, not the story's.
// internal/config imports agentcli, so the root is resolved here and passed in.
var driverSnapshotter = func(harness, sessionID, repoRoot string) agentcli.DriverSnapshot {
	return agentcli.SessionUsageSnapshotWith(harness, sessionID, repoRoot, []string{config.PublishedSessionRoot(sessionID)})
}

// Trigger names for DriverUsagePayload.Trigger — which kind of enacted
// transition produced this snapshot (sty_81caa41b AC3).
const (
	DriverTriggerEngage     = "engage"
	DriverTriggerTransition = "transition"
	DriverTriggerPark       = "park"
	DriverTriggerClose      = "close"
	DriverTriggerLate       = "late"
)

// driverCumulative is the harness-reported running total at one snapshot — the
// high-water mark the NEXT snapshot for the same session diffs against.
type driverCumulative struct {
	FreshInput int      `json:"fresh_input"`
	CacheRead  int      `json:"cache_read"`
	CacheWrite int      `json:"cache_write"`
	Output     int      `json:"output"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`
	// ModelCalls is the session's absolute count of model requests at this
	// snapshot; nil when the harness reports none (or the row predates the
	// field) — a difference of two rows is only a count when both carry it.
	ModelCalls *int `json:"model_calls,omitempty"`
}

// cumulativeFromSnap is the harness-reported running total of one snapshot.
func cumulativeFromSnap(s agentcli.DriverSnapshot) driverCumulative {
	c := driverCumulative{
		FreshInput: s.FreshInputTokens, CacheRead: s.CacheReadInputTokens,
		CacheWrite: s.CacheCreationInputTokens, Output: s.OutputTokens, CostUSD: s.CostUSD,
	}
	if s.ModelCallsUnavailableReason == "" {
		n := s.ModelCalls
		c.ModelCalls = &n
	}
	return c
}

// stampModelCalls records on p the model requests made between base and cur, or
// the adapter-named reason it cannot: a harness whose record has no count, or a
// base row written before the count existed. Never a zero that reads as a
// measurement (satelle-agent-agnostic §2).
func stampModelCalls(p *DriverUsagePayload, snap agentcli.DriverSnapshot, cur, base driverCumulative) {
	switch {
	case snap.ModelCallsUnavailableReason != "":
		p.ModelCallsUnavailableReason = snap.ModelCallsUnavailableReason
	case p.BaselineFresh:
		p.ModelCallsUnavailableReason = costview.BaselineFreshReason(p.Executable)
	case cur.ModelCalls == nil || base.ModelCalls == nil:
		p.ModelCallsUnavailableReason = "driver_usage: the base row predates model-call recording"
	default:
		n := *cur.ModelCalls - *base.ModelCalls
		p.ModelCalls = &n
	}
}

// DriverUsagePayload is the driver_usage ledger row's payload shape: the
// driving session's measured delta since its previous snapshot for the same
// session, or an adapter-named unavailable reason (sty_81caa41b AC1). The
// ledger is append-only: no row is ever rewritten. WindowKey plus Cumulative
// are what a later snapshot compares against to decide it is a no-op (AC7).
type DriverUsagePayload struct {
	SessionID  string `json:"session_id"`
	Executable string `json:"executable"`
	Model      string `json:"model,omitempty"`

	FreshInput int `json:"fresh_input"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Output     int `json:"output"`

	CostUSD               *float64 `json:"cost_usd,omitempty"`
	CostUnavailableReason string   `json:"cost_unavailable_reason,omitempty"`

	// ModelCalls is the model requests this row's window made (the delta, like
	// the token fields); nil with ModelCallsUnavailableReason when the harness
	// cannot say (sty_c4b92c9e).
	ModelCalls                  *int   `json:"model_calls,omitempty"`
	ModelCallsUnavailableReason string `json:"model_calls_unavailable_reason,omitempty"`

	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`

	WallSeconds float64 `json:"wall_seconds"`
	Trigger     string  `json:"trigger"`
	From        string  `json:"from,omitempty"`
	To          string  `json:"to,omitempty"`

	Cumulative driverCumulative `json:"cumulative"`
	WindowKey  string           `json:"window_key"`

	Late bool `json:"late,omitempty"`
	// Unflushed marks a row read from a harness that records a turn's usage only
	// when the turn ends (agentcli.DriverSnapshot.MayUndercountInFlightTurn): its
	// cumulative excludes the turn that was running when it was read. A gate
	// wait's count is only readable from a row taken after that turn flushed.
	Unflushed bool `json:"unflushed,omitempty"`
	// Pending marks a close/park row read from a harness that reported
	// agentcli.DriverSnapshot.MayUndercountInFlightTurn (grok — NEVER
	// claude, see that field's doc comment) — the read may undercount the
	// very turn that made the transition call, because that harness's session
	// record only flushes a turn's usage once the turn fully completes.
	// Confirmed by the next read for this session, whatever triggers it (the
	// next transition, or sweepPendingDriverUsage) — but ONLY the delta for
	// the exact ONE turn that was in flight at this read is ever swept onto
	// this same story as a Late row (Turns proves it, see
	// recordLateDriverUsageCatchup): unrelated activity from further turns
	// that may have piled up before the confirming read is never guessed at.
	Pending bool `json:"pending,omitempty"`
	// Turns is the harness-reported turn count this row's Cumulative
	// reflects (agentcli.DriverSnapshot.Turns) — what a later Pending
	// catch-up compares against to prove it is isolating exactly the one
	// turn that was in flight, not an unknown number of turns' worth of
	// unrelated activity.
	Turns int `json:"turns,omitempty"`
	// BaseTurns is the harness Turns count of whatever Cumulative this row's
	// delta was diffed AGAINST (0 when the base was a fresh/current-cumulative
	// baseline rather than a prior row's own Cumulative) — together with Turns
	// it names the exact turn-index RANGE [BaseTurns, Turns) this row's delta
	// covers. EVERY delta-carrying row sets this — ordinary, the single-turn
	// Pending catch-up, the whole-gap Late catch-up and the Kill row alike
	// (sty_81caa41b Revision 5, Rule T generalised) — because sessionClaimedTurns
	// is the ONE shared check every one of them consults before claiming a
	// range, and it works only if every writer's own range is recorded the
	// same way. A row whose base came from `fresh` (no historical range
	// attributed) sets BaseTurns to that same snapshot's own Turns, i.e. an
	// empty range — see recordDriverUsage. A legacy row written before this
	// field existed carries BaseTurns=Turns=0 with no breakdown behind it, so
	// its range is empty too — it claims nothing.
	BaseTurns int `json:"base_turns,omitempty"`
	// CreditedTurns names the exact session-wide turn indices THIS Late row
	// claims (sty_81caa41b Revision 4, Rule T) — always a single index in the
	// current implementation (recordPendingCatchupFromTurnBreakdown resolves
	// one Pending row at a time), kept as a slice for whatever a future sweep
	// might batch. Redundant with [BaseTurns,Turns) for that row (Revision 5
	// generalised the same information onto every writer), kept as its own
	// field because it is what a test or a human reading the ledger reads
	// directly, without having to reconstruct "was this row a single-turn
	// credit" from the range shape.
	CreditedTurns []int `json:"credited_turns,omitempty"`
	// BaselineFresh marks the first available row after one or more unavailable
	// rows: its base is this read's own cumulative because there is no prior
	// measurement to diff against, so its zero token and model-call delta is "the
	// baseline was set here", never a measured zero. It is distinct from
	// Unflushed/Pending: a flush-lagged harness's missing turn and a fresh
	// baseline both read as an all-zero delta, and only this marker lets a later
	// reader tell them apart (sty_89768625). A session's very first row is the
	// ordinary engage baseline and is not marked.
	BaselineFresh bool `json:"baseline_fresh,omitempty"`
	// Backfilled marks a row written after the fact by story-driver-backfill: its
	// figures are the session record's per-message timestamps attributed to
	// [WindowFrom, WindowTo] — derived, never a live-measured delta. It carries no
	// Cumulative and claims no turn range, so it is invisible to the live baseline,
	// the claimed-turn set and the session total (sty_8c0e7e8c).
	Backfilled bool   `json:"backfilled,omitempty"`
	WindowFrom string `json:"window_from,omitempty"`
	WindowTo   string `json:"window_to,omitempty"`
	// Overlaps names the other stories that drove this row's session in a window
	// intersecting the story's own driver window (sessionWindowOverlaps): the
	// session's usage cannot be split between them, so costview reads the row as
	// ambiguous. AttributionReason is the adapter-named sentence saying so —
	// "<adapter>: window overlaps <ids> in session <id>; usage not split" — joined
	// with the adapter's subagent qualifier (agentcli.DriverSnapshot.SubagentNote)
	// when it has one; a qualifier alone leaves the row exact.
	Overlaps          []string `json:"overlaps,omitempty"`
	AttributionReason string   `json:"attribution_reason,omitempty"`
}

// driverUsageTrigger classifies the enacted transition from → to for
// DriverUsagePayload.Trigger, reusing the same shape-derived predicates the
// engagement code already applies (statusIsParkState, targetIsExitState,
// storyStatusIsEngaging) rather than a second status-literal classification.
// Engage means entering an engaged state from one that was not engaged; a move
// between two engaged states (in_progress → integration) is a transition.
func driverUsageTrigger(ctx context.Context, item workitem.Item, from, to string) string {
	if statusIsParkState(ctx, item, to) {
		return DriverTriggerPark
	}
	if targetIsExitState(ctx, item, to) {
		return DriverTriggerClose
	}
	if engaging, ok := storyStatusIsEngaging(ctx, item, to); ok && engaging {
		if wasEngaged, ok := storyStatusIsEngaging(ctx, item, from); !ok || !wasEngaged {
			return DriverTriggerEngage
		}
	}
	return DriverTriggerTransition
}

// recordDriverUsage snapshots the driving session's usage and, when it moved
// since the last snapshot recorded for this session (any story), appends one
// driver_usage row on item.ID (sty_81caa41b AC1/AC3/AC4/AC7). Best-effort:
// never blocks the transition it is called from — errors resolving the
// session or reading the record land as an available:false row via the
// snapshotter, not a Go error the caller has to handle.
//
// An engagement with no transition ever following it (round 1's named gap)
// is DELIBERATELY left unseeded: a coder-round-2 attempt at an engage-time
// baseline hook (called from the engagement path in single_story.go, before
// any guarded transition write) broke TestTransitionAppendFailureRollsBackRow
// — a pre-commit best-effort Append there shares the same ledgerStore as the
// guarded transition write, so it can consume a transient failure meant for
// that write instead of the transition's own. The gap it would have closed is
// also already absorbed gracefully by this function's own first-snapshot
// fallback below: a session's usage before its first recorded transition
// becomes the baseline every later call diffs against, which AC9's
// reconciliation reports as unattributed rather than losing it silently.
// Closing the round-1 gap exactly as named would need a write path that
// cannot compete with the guarded transition's own append — worth a
// dedicated design, not a bolt-on on the engage hot path.
//
// A killed session cannot call back into its own transition path, so its last
// stretch is recorded only when a later snapshot of the same session reads it.
// AC6 (late-become-readable usage) covers
// TWO distinct gaps, both handled below via the same Late-row mechanism, but
// with different confidence bounds:
//   - a record that failed to read outright (Available=false) and later
//     becomes readable: the WHOLE gap since the last known-good point is
//     attributed to the story the unreadable row belonged to (there is
//     nothing better to go on — the record carried literally no data for
//     that window).
//   - a record that read SUCCESSFULLY at close/park but, on a harness with
//     agentcli.DriverSnapshot.MayUndercountInFlightTurn (grok — never
//     claude), may still be an UNDERCOUNT of the very turn that made the
//     call, because that harness only flushes a turn's cumulative once the
//     turn fully completes. Such a row is stamped Pending. Unlike the
//     unavailable case, the confirming read for a Pending row is NOT trusted
//     to attribute its whole gap to the closed story — that gap could span
//     an unknown amount of unrelated later session activity (a different
//     story's own engage-turn reasoning, a sweep firing much later, …), and
//     crediting all of it to the story that merely closed first would just
//     trade one misattribution for another. Instead the catch-up is bounded
//     to EXACTLY the one turn that was in flight at the Pending read,
//     verified via the harness's own Turns counter (recordLateDriverUsageCatchup):
//     if it advanced by exactly 1, that turn's delta is the in-flight one and
//     is swept onto the closed story; any other amount is left unresolved
//     (the row stays Pending for a later, tighter-timed read) rather than
//     guessed at.
//
// Either way, the current call's own base always starts fresh at this read —
// never inheriting a Pending/unavailable row's cumulative — so an
// unresolved gap becomes AC9's honest "unattributed" remainder, never
// silently folded into whatever story happens to take the next snapshot.
//
// STILL NOT IMPLEMENTED: AC8's forked-session-id-with-parent-reference resume
// case — confirmed against real captured session records (see
// internal/agentcli/testdata/driver/README.md), no adapter in this codebase
// (claude transcript, grok usage.json) surfaces a parent/
// resume lineage for a NEW session id to seed a baseline from. This is a
// genuine data gap in the harness's own record, not an unwritten branch:
// there is nothing to read. AC8's SAME-id resume case needs no extra code:
// this function's existing per-session high-water mark already attributes
// only the usage after whatever the last recorded snapshot saw, resume or
// not — pinned by TestRecordDriverUsageResumeSameSessionIDAttributesOnlyNewUsage.
// driverSessionHarness names the harness whose record holds sessionID's usage.
// The session's own published in-loop row wins: its hooks name their harness
// (sty_719c4a7b), while the environment can carry a parent agent's markers —
// a grok session started from a Claude Code shell inherits CLAUDECODE=1.
// The environment is the fallback when the session published nothing.
func driverSessionHarness(sessionID string) string {
	switch _, exe, _ := config.ResolveSessionModel(sessionID, SessionModelRoleInLoop); exe {
	case agentcli.HarnessClaude, agentcli.HarnessGrok, agentcli.HarnessPi, agentcli.HarnessCursor:
		return exe
	}
	if h, ok := agentcli.InLoopHarnessFromEnv(os.Environ()); ok {
		return h
	}
	return agentcli.HarnessUnknown
}

func recordDriverUsage(ctx context.Context, item workitem.Item, from, to string, now time.Time) {
	sessionID := config.ResolveSession()
	if sessionID == "" {
		return // no session identity to attribute usage to (ordinary unstamped use)
	}
	recordDriverUsageAs(ctx, item, sessionID, driverSessionHarness(sessionID), driverUsageTrigger(ctx, item, from, to), from, to, now)
}

// recordDriverUsageAs is recordDriverUsage with the session, its harness and the
// trigger already decided — a transition's trigger is derived from its edge, a
// gate wait's is DriverTriggerGate, and a gate wait may be settled by a process
// that is not the driving session.
func recordDriverUsageAs(ctx context.Context, item workitem.Item, sessionID, harness, trigger, from, to string, now time.Time) {
	if ledgerStore == nil || strings.TrimSpace(item.ID) == "" || sessionID == "" {
		return
	}

	prev, prevFound, prevAvail, prevAvailFound := sessionDriverUsageState(ctx, sessionID)
	snap := driverSnapshotter(harness, sessionID, changeRecordRepoRoot())

	payload := DriverUsagePayload{
		SessionID: sessionID, Executable: harness, Model: snap.Model,
		Trigger: trigger, From: from, To: to,
		Unflushed: snap.MayUndercountInFlightTurn,
	}
	payload.WindowKey = fmt.Sprintf("%s|%s|%s|%s|%s", item.ID, sessionID, trigger, from, to)
	if prevFound {
		payload.WallSeconds = now.Sub(prev.at).Seconds()
	}

	if !snap.Available {
		payload.UnavailableReason = snap.UnavailableReason
		payload.CostUnavailableReason = snap.CostUnavailableReason
		// R2: a later snapshot with the SAME reason for the SAME window writes
		// nothing further — an unreadable record snapshotted N times gives one
		// row, not N (AC4/AC7).
		if prevFound && prev.payload.WindowKey == payload.WindowKey && !prev.payload.Available &&
			prev.payload.UnavailableReason == payload.UnavailableReason {
			return
		}
		appendDriverUsageRow(ctx, item.ID, payload, now)
		return
	}

	payload.Available = true
	payload.Turns = snap.Turns
	fresh := cumulativeFromSnap(snap)
	payload.Cumulative = fresh
	// A retry/re-invocation of the SAME transition (identical WindowKey) must
	// reuse prev's own cumulative as base regardless of Pending — retrying A's
	// OWN close is not "the next snapshot after A", so it must not trigger a
	// late catch-up onto A while A's close row is still what is being computed.
	sameWindow := prevFound && prev.payload.WindowKey == payload.WindowKey
	var base driverCumulative
	baseFromPriorCumulative := false
	switch {
	case sameWindow:
		if prev.payload.Available {
			base = prev.payload.Cumulative
			payload.BaseTurns = prev.payload.Turns
			baseFromPriorCumulative = true
		} else {
			base = fresh
			payload.BaseTurns = snap.Turns
			payload.BaselineFresh = true
		}
	case prevFound && !prev.payload.Available:
		// AC6: the previous row for this session failed to read outright — the
		// record carried nothing for that whole window, so the entire gap since
		// the last KNOWN-GOOD point is caught up as a Late row on the story the
		// unavailable row belonged to (prev.storyID — often a story that has
		// since closed), never silently folded into whatever story happens to
		// take this snapshot.
		recordLateDriverUsageCatchup(ctx, sessionID, harness, snap, fresh, prev, prevAvail, prevAvailFound, false, now)
		base = fresh
		payload.BaseTurns = snap.Turns
		payload.BaselineFresh = true
	case prevFound && prev.payload.Pending:
		// AC6: the previous row read successfully but was Pending — it MAY have
		// undercounted its own in-flight turn (grok only). Bounded catch-up
		// only: recordLateDriverUsageCatchup attributes usage to the closed story
		// ONLY when the harness's Turns counter proves this read isolates exactly
		// that one turn — never the whole gap, which could include unrelated
		// later activity this read has no way to tell apart from the lagged one.
		recordLateDriverUsageCatchup(ctx, sessionID, harness, snap, fresh, prev, prevAvail, prevAvailFound, true, now)
		base = fresh
		payload.BaseTurns = snap.Turns
	case prevFound && prev.payload.Available:
		base = prev.payload.Cumulative
		payload.BaseTurns = prev.payload.Turns
		baseFromPriorCumulative = true
	default:
		// No prior row exists for this session at all: seed the baseline from
		// the CURRENT cumulative so usage the session already accrued before
		// this snapshot — e.g. a long-running session's earlier, unattributed
		// work — is never counted as a giant delta onto whichever story happens
		// to take the next snapshot. This is also what keeps a fresh engage from
		// inheriting the PREVIOUS story's tail (AC3): once that story's own
		// close row lands as the session's latest AVAILABLE mark, this branch is
		// not taken for the next story.
		base = fresh
		payload.BaseTurns = snap.Turns
	}
	if baseFromPriorCumulative && len(snap.TurnBreakdown) > 0 {
		// Rule T, generalised (sty_81caa41b Revision 5): this row's
		// [BaseTurns,Turns) range may span a turn index some OTHER
		// delta-carrying row — ordinary, Pending, Late or Kill alike — has
		// already claimed. Advancing the base past any already-claimed turn's
		// own contribution excludes it from THIS row's delta, so it is never
		// counted twice.
		base = adjustBaseForClaimedTurns(base, snap.TurnBreakdown, payload.BaseTurns, snap.Turns, sessionClaimedTurns(ctx, sessionID))
	}
	payload.FreshInput = snap.FreshInputTokens - base.FreshInput
	payload.CacheRead = snap.CacheReadInputTokens - base.CacheRead
	payload.CacheWrite = snap.CacheCreationInputTokens - base.CacheWrite
	payload.Output = snap.OutputTokens - base.Output
	stampModelCalls(&payload, snap, fresh, base)
	if snap.CostUSD != nil {
		cost := *snap.CostUSD
		if base.CostUSD != nil {
			cost -= *base.CostUSD
		}
		payload.CostUSD = &cost
	} else {
		payload.CostUnavailableReason = snap.CostUnavailableReason
	}
	if (trigger == DriverTriggerClose || trigger == DriverTriggerPark) && snap.MayUndercountInFlightTurn {
		// AC6: this harness (grok, never claude — see
		// agentcli.DriverSnapshot.MayUndercountInFlightTurn) may not have flushed
		// the very turn that made this call yet. Leave it open for the next read
		// to confirm — bounded to that one turn only, see
		// recordLateDriverUsageCatchup.
		payload.Pending = true
	}

	// R1: a retried transition, or a snapshot whose cumulative has not moved
	// since the same window's last row, writes nothing (AC7).
	if prevFound && prev.payload.WindowKey == payload.WindowKey && prev.payload.Available &&
		cumulativeEqual(prev.payload.Cumulative, payload.Cumulative) {
		return
	}
	// The figures stay (the row is still the delta baseline, so the next delta does
	// not re-credit this spend); the attribution says they are not exact.
	stampDriverAttribution(ctx, item, &payload, snap.SubagentNote, now)
	appendDriverUsageRow(ctx, item.ID, payload, now)
}

func cumulativeEqual(a, b driverCumulative) bool {
	if a.FreshInput != b.FreshInput || a.CacheRead != b.CacheRead || a.CacheWrite != b.CacheWrite || a.Output != b.Output {
		return false
	}
	if (a.CostUSD == nil) != (b.CostUSD == nil) {
		return false
	}
	return a.CostUSD == nil || *a.CostUSD == *b.CostUSD
}

type driverUsageRow struct {
	payload DriverUsagePayload
	storyID string
	at      time.Time
}

// sessionDriverUsageState returns sessionID's most recent driver_usage row of
// ANY availability (latest) and, separately, its most recent AVAILABLE row
// (latestAvail) — the true last-known-good point AC6's late catch-up diffs
// against, which differs from latest exactly when the newest row is an
// unavailable gap. The session-level mark is what keeps two consecutive
// stories' deltas separate (AC3).
//
// Scans via ledgerStore.ForEachKind, which pages internally with no upper
// bound — NOT ledgerStore.List(...,Limit:2000). A bounded List call orders
// oldest-first: once total driver_usage rows across every session exceed the
// cap, it silently returns the OLDEST 2000 and drops the newest ones — the
// exact rows a "most recent" lookup needs — corrupting every baseline this
// function feeds once the ledger grows past that size. ForEachKind is the
// same unbounded-scan fix already applied to the --all skill roll-up
// (sty_363eaf55) for the identical failure mode.
func sessionDriverUsageState(ctx context.Context, sessionID string) (latest driverUsageRow, latestFound bool, latestAvail driverUsageRow, latestAvailFound bool) {
	if ledgerStore == nil {
		return driverUsageRow{}, false, driverUsageRow{}, false
	}
	_ = ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || p.SessionID != sessionID || p.Backfilled {
			return nil // a backfilled row is history, not the live session's high-water mark
		}
		row := driverUsageRow{payload: p, storyID: e.StoryID, at: e.CreatedAt}
		latest, latestFound = row, true
		if p.Available {
			latestAvail, latestAvailFound = row, true
		}
		return nil
	})
	return
}

// recordLateDriverUsageCatchup writes the AC6 Late row: prev (the session's
// latest row, or a still-open Pending row a sweep found) was either
// unavailable, or an available-but-Pending close/park read that may have
// undercounted its own turn — either way, some usage since the last
// KNOWN-GOOD point may not have been safely attributed. Written onto
// prev.storyID, the story the pending/unavailable row belonged to, which may
// already be closed — never onto whatever story happens to be mid-transition
// right now (that story's own row is computed separately by the caller,
// starting fresh from this same snapshot).
//
// boundToSingleTurn (true only for the Pending case, never the unavailable
// one — see the two call sites in recordDriverUsage and the sweep) delegates
// to recordPendingCatchupFromTurnBreakdown, which credits EXACTLY the one
// turn that was in flight when prev was written — never any later turn, which
// by construction started only after prev's own close/park call and is
// unrelated activity (AC3/AC9). The unavailable case has no such bound: the
// record carried nothing at all for that window, so there is no "unrelated
// activity" risk to guard against — the whole gap IS that story's.
func recordLateDriverUsageCatchup(ctx context.Context, sessionID, harness string, snap agentcli.DriverSnapshot, fresh driverCumulative, prev, prevAvail driverUsageRow, prevAvailFound, boundToSingleTurn bool, now time.Time) {
	if strings.TrimSpace(prev.storyID) == "" {
		return
	}
	if boundToSingleTurn {
		recordPendingCatchupFromTurnBreakdown(ctx, sessionID, harness, snap, prev, now)
		return
	}
	lateBase := fresh
	lateBaseTurns := snap.Turns
	switch {
	case prev.payload.Available:
		lateBase = prev.payload.Cumulative
		lateBaseTurns = prev.payload.Turns
	case prevAvailFound:
		lateBase = prevAvail.payload.Cumulative
		lateBaseTurns = prevAvail.payload.Turns
	}
	if len(snap.TurnBreakdown) > 0 {
		// sty_81caa41b Revision 5: this gap may span a turn index some OTHER
		// delta-carrying row (a kill row, a sibling story's ordinary delta)
		// already claimed between lateBaseTurns and now — exclude it rather
		// than counting it a second time onto this gap-late row.
		lateBase = adjustBaseForClaimedTurns(lateBase, snap.TurnBreakdown, lateBaseTurns, snap.Turns, sessionClaimedTurns(ctx, sessionID))
	}
	late := DriverUsagePayload{
		SessionID: sessionID, Executable: harness, Model: snap.Model,
		Trigger: DriverTriggerLate, To: prev.payload.To, Late: true,
		Available:   true,
		WindowKey:   prev.payload.WindowKey + "|late",
		WallSeconds: now.Sub(prev.at).Seconds(),
		Cumulative:  fresh,
		Turns:       snap.Turns,
		BaseTurns:   lateBaseTurns,
	}
	late.FreshInput = fresh.FreshInput - lateBase.FreshInput
	late.CacheRead = fresh.CacheRead - lateBase.CacheRead
	late.CacheWrite = fresh.CacheWrite - lateBase.CacheWrite
	late.Output = fresh.Output - lateBase.Output
	stampModelCalls(&late, snap, fresh, lateBase)
	if fresh.CostUSD != nil {
		c := *fresh.CostUSD
		if lateBase.CostUSD != nil {
			c -= *lateBase.CostUSD
		}
		late.CostUSD = &c
	} else {
		late.CostUnavailableReason = snap.CostUnavailableReason
	}
	late.AttributionReason = snap.SubagentNote
	if late.FreshInput == 0 && late.CacheRead == 0 && late.CacheWrite == 0 && late.Output == 0 && late.CostUSD == nil {
		return
	}
	appendDriverUsageRow(ctx, prev.storyID, late, now)
}

// recordPendingCatchupFromTurnBreakdown resolves a Pending close/park row
// (prev) by crediting the story it belongs to with EXACTLY the one turn that
// was still running when that row was written — agentcli.DriverSnapshot
// .TurnBreakdown[prev.payload.Turns] in the CONFIRMING read (snap), never any
// further turn (sty_81caa41b AC6). Turns only advances by a turn completing,
// so the turn at that index is, by construction, the one that started before
// or at the close and finished sometime after — every later index started
// only once that one had already finished, i.e. after the close, and is
// unrelated activity this story never owned. That remainder is deliberately
// left uncredited to anyone: AC9's reconciliation reports it as unattributed
// rather than this function guessing whose it was.
//
// If the confirming read's breakdown does not yet reach that index, the turn
// has not completed yet — prev is left Pending for a later, tighter-timed
// read (another transition, or a sweep) to resolve.
//
// Rule T, generalised (sty_81caa41b Revision 5): a turn index a fast-moving
// driving session can also close a SIBLING story inside the same underlying
// harness turn — two Pending rows, from two different stories, both naming
// the same idx — or have it claimed first by a kill row (a dead session's
// usage closed before this resolution ran) or a gap-late row. Before crediting, this
// checks the ONE shared claimed-turn set every delta-carrying row consults
// (sessionClaimedTurns: the union of every AVAILABLE row's own
// [BaseTurns,Turns) range, ordinary/Pending/Late/Kill alike). Whichever
// writer reaches idx FIRST keeps it; this resolution backs off rather than
// double-count it.
func recordPendingCatchupFromTurnBreakdown(ctx context.Context, sessionID, harness string, snap agentcli.DriverSnapshot, prev driverUsageRow, now time.Time) {
	idx := prev.payload.Turns
	if idx < 0 || idx >= len(snap.TurnBreakdown) {
		return
	}
	if sessionClaimedTurns(ctx, sessionID)[idx] {
		return
	}
	t := snap.TurnBreakdown[idx]
	if t.FreshInput == 0 && t.CacheRead == 0 && t.CacheWrite == 0 && t.Output == 0 && t.CostUSD == nil {
		return
	}
	late := DriverUsagePayload{
		SessionID: sessionID, Executable: harness, Model: snap.Model,
		Trigger: DriverTriggerLate, To: prev.payload.To, Late: true,
		Available:     true,
		WindowKey:     prev.payload.WindowKey + "|late",
		WallSeconds:   now.Sub(prev.at).Seconds(),
		Cumulative:    cumulativeFromSnap(snap),
		BaseTurns:     idx,
		Turns:         idx + 1,
		CreditedTurns: []int{idx},
		FreshInput:    t.FreshInput, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output,
		CostUSD:           t.CostUSD,
		AttributionReason: snap.SubagentNote,
	}
	// The credited turn's own request count, from the same breakdown entry.
	if snap.ModelCallsUnavailableReason != "" {
		late.ModelCallsUnavailableReason = snap.ModelCallsUnavailableReason
	} else {
		n := t.ModelCalls
		late.ModelCalls = &n
	}
	appendDriverUsageRow(ctx, prev.storyID, late, now)
}

// sessionClaimedTurns is the ONE shared check every delta-carrying
// driver_usage writer consults before claiming a turn index — ordinary,
// Pending, Late (both the single-turn Pending catch-up and the whole-gap
// catch-up) and Kill rows alike (sty_81caa41b Revision 5). It unions the
// [BaseTurns,Turns) range of every AVAILABLE row recorded for sessionID: that
// range is exactly the set of harness turn indices that row's own delta
// already counts, whatever kind of row it is. A legacy row written before
// BaseTurns/Turns existed carries both as zero with no breakdown behind it,
// so its range is empty — it claims nothing, per Revision 5 §2.
//
// Two disjoint session turn indices are never both attributed to the same
// story by two different rows so long as every writer both (a) checks this
// set before claiming a range and (b) is itself included in this scan once
// written — whichever writer reaches an index FIRST keeps it; every later
// writer's own check finds it already claimed and backs off.
func sessionClaimedTurns(ctx context.Context, sessionID string) map[int]bool {
	claimed := map[int]bool{}
	if ledgerStore == nil {
		return claimed
	}
	_ = ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || p.SessionID != sessionID || !p.Available || p.Backfilled {
			return nil
		}
		for idx := p.BaseTurns; idx < p.Turns; idx++ {
			claimed[idx] = true
		}
		return nil
	})
	return claimed
}

// adjustBaseForClaimedTurns advances base past every turn index in
// [fromTurn, toTurn) that claimed already covers, adding that turn's own
// breakdown contribution into the returned base so the caller's subsequent
// `current - base` delta excludes it (sty_81caa41b Revision 5, Rule T
// generalised to every writer via sessionClaimedTurns).
func adjustBaseForClaimedTurns(base driverCumulative, breakdown []agentcli.DriverTurn, fromTurn, toTurn int, claimed map[int]bool) driverCumulative {
	if len(claimed) == 0 || len(breakdown) == 0 {
		return base
	}
	limit := toTurn
	if limit > len(breakdown) {
		limit = len(breakdown)
	}
	adjusted := base
	for idx := fromTurn; idx < limit; idx++ {
		if !claimed[idx] {
			continue
		}
		t := breakdown[idx]
		adjusted.FreshInput += t.FreshInput
		adjusted.CacheRead += t.CacheRead
		adjusted.CacheWrite += t.CacheWrite
		adjusted.Output += t.Output
		if adjusted.ModelCalls != nil {
			n := *adjusted.ModelCalls + t.ModelCalls
			adjusted.ModelCalls = &n
		}
		if t.CostUSD != nil {
			v := 0.0
			if adjusted.CostUSD != nil {
				v = *adjusted.CostUSD
			}
			v += *t.CostUSD
			adjusted.CostUSD = &v
		}
	}
	return adjusted
}

// sweepPendingDriverUsage confirms every driver_usage row still Pending
// anywhere in the ledger (sty_81caa41b AC6) — a close/park row that may have
// undercounted its own turn on grok. Inline catch-up inside
// recordDriverUsage only fires on the SAME session's NEXT transition; this
// sweep also covers two gaps that leaves: a session that never drives another
// story after closing this one (that tail would otherwise never get
// confirmed, since there is no daemon in this binary to notice on its own),
// and a Pending row that a LATER row (a kill row, or another story's own
// transition) has pushed off the session's "latest" position without ever
// resolving it — the inline path only ever looks at the CURRENT latest row,
// so a Pending row buried under later rows would be invisible to it forever.
// Scanning for every Pending row without a matching "<window_key>|late" row,
// rather than only each session's latest row, finds it regardless. Called
// from the UI snapshot — an existing, frequently-invoked touchpoint — so a
// real "no next transition" tail still gets swept given enough ordinary
// `satelle` usage, without a dedicated scheduler. Idempotent: once a Pending
// row gains a matching late row (or is found to have no further growth yet),
// a repeat sweep skips it.
func sweepPendingDriverUsage(ctx context.Context, now time.Time) {
	if ledgerStore == nil {
		return
	}
	pendingByKey := map[string]driverUsageRow{} // window_key -> the still-open Pending row
	resolvedLateWindowKeys := map[string]bool{}
	_ = ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || strings.TrimSpace(p.SessionID) == "" {
			return nil
		}
		if p.Pending {
			pendingByKey[p.WindowKey] = driverUsageRow{payload: p, storyID: e.StoryID, at: e.CreatedAt}
		}
		if p.Late {
			resolvedLateWindowKeys[p.WindowKey] = true
		}
		return nil
	})
	pending := make([]driverUsageRow, 0, len(pendingByKey))
	for _, row := range pendingByKey {
		pending = append(pending, row)
	}
	// Rule T (sty_81caa41b Revision 4): process the earliest-CREATED Pending
	// row first. When two stories each close inside the same underlying
	// harness turn, both their Pending rows name the SAME turn index — the
	// one that closed FIRST is the one that gets credited; recordPendingCatchup
	// FromTurnBreakdown's own sessionCreditedTurns check then finds the index
	// already claimed for every row processed after it in this same pass.
	sort.Slice(pending, func(i, j int) bool { return pending[i].at.Before(pending[j].at) })
	for _, prev := range pending {
		windowKey := prev.payload.WindowKey
		if resolvedLateWindowKeys[windowKey+"|late"] {
			continue
		}
		harness := prev.payload.Executable
		if strings.TrimSpace(harness) == "" {
			harness = agentcli.HarnessUnknown
		}
		snap := driverSnapshotter(harness, prev.payload.SessionID, changeRecordRepoRoot())
		if !snap.Available {
			continue // cannot confirm yet; stays Pending for a later sweep/transition
		}
		recordPendingCatchupFromTurnBreakdown(ctx, prev.payload.SessionID, harness, snap, prev, now)
	}
}

// SweepPendingDriverUsage gives any still-open Pending driver_usage row a
// chance to settle (AC6) from a caller outside package verb (see
// sweepPendingDriverUsage's doc comment).
func SweepPendingDriverUsage(ctx context.Context, now time.Time) {
	sweepPendingDriverUsage(ctx, now)
}

// SessionReconciliationStory is one story's attributed slice of a session's
// driver usage, summed across every driver_usage row that story carries for
// that session (sty_81caa41b AC9). A story with even one unavailable row is
// reported unavailable in full — a partial sum is not a trustworthy figure.
type SessionReconciliationStory struct {
	StoryID string `json:"story_id"`

	FreshInput int      `json:"fresh_input"`
	CacheRead  int      `json:"cache_read"`
	CacheWrite int      `json:"cache_write"`
	Output     int      `json:"output"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`

	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	// Backfilled is true when part of the figure is a backfill row: derived from
	// session-record timestamps, not a live-measured delta.
	Backfilled bool `json:"backfilled,omitempty"`
}

// SessionReconciliation reports, for one driving session, the harness's own
// latest known cumulative total against the sum of what was attributed to
// every story it drove — the gap is unattributed usage (e.g. session time
// between one story's close and the next story's engage baseline, sty_81caa41b
// AC9). Unavailable amounts are never folded into a sum as zero: an
// unavailable total, or any unavailable story, makes the remainder itself
// UnattributedPartial rather than a number that looks precise but is not.
type SessionReconciliation struct {
	SessionID  string `json:"session_id"`
	Executable string `json:"executable"`

	Stories []SessionReconciliationStory `json:"stories"`

	TotalAvailable         bool             `json:"total_available"`
	TotalUnavailableReason string           `json:"total_unavailable_reason,omitempty"`
	Total                  driverCumulative `json:"total"`

	UnattributedAvailable bool             `json:"unattributed_available"`
	UnattributedPartial   bool             `json:"unattributed_partial"`
	Unattributed          driverCumulative `json:"unattributed"`
}

// ComputeSessionReconciliation sums every driver_usage row recorded for
// sessionID, grouped by the story it landed on, against the session's latest
// known cumulative — the same session-wide scan sessionDriverUsageState runs,
// kept here as its own pass because it needs every row, not just the newest.
//
// Scans via ForEachKind (unbounded, paginated), not List(...,Limit:2000): see
// sessionDriverUsageState's doc comment — the same oldest-first truncation
// would silently drop this session's newest rows, and its OLD ones for a
// long-lived session, once total driver_usage rows exceed the cap.
func ComputeSessionReconciliation(ctx context.Context, sessionID string) (SessionReconciliation, error) {
	recon := SessionReconciliation{SessionID: sessionID}
	sessionID = strings.TrimSpace(sessionID)
	if ledgerStore == nil {
		return recon, fmt.Errorf("verb: no ledger store wired")
	}
	if sessionID == "" {
		return recon, fmt.Errorf("verb: no session id given")
	}

	type storyAgg struct {
		sum               driverCumulative
		available         bool
		unavailableReason string
		seen              bool
		backfilled        bool
	}
	byStory := map[string]*storyAgg{}
	var order []string
	var latestAt time.Time
	var haveLatest bool

	type reconRow struct {
		p       DriverUsagePayload
		storyID string
		at      time.Time
	}
	var rows []reconRow
	// backfilledAt is, per story, when its latest AVAILABLE backfill row was written:
	// every unavailable row the story carried before that is superseded by it.
	backfilledAt := map[string]time.Time{}

	err := ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || p.SessionID != sessionID {
			return nil
		}
		rows = append(rows, reconRow{p: p, storyID: e.StoryID, at: e.CreatedAt})
		if p.Backfilled && p.Available && e.CreatedAt.After(backfilledAt[e.StoryID]) {
			backfilledAt[e.StoryID] = e.CreatedAt
		}
		return nil
	})
	if err != nil {
		return recon, err
	}
	for _, r := range rows {
		p := r.p
		if recon.Executable == "" {
			recon.Executable = p.Executable
		}
		// The session total is the harness's own latest cumulative: a backfilled row
		// carries none, and writing it later must not make it the session's latest read.
		if !p.Backfilled && (!haveLatest || r.at.After(latestAt)) {
			haveLatest = true
			latestAt = r.at
			recon.TotalAvailable = p.Available
			recon.TotalUnavailableReason = p.UnavailableReason
			if p.Available {
				recon.Total = p.Cumulative
			}
		}
		if at, ok := backfilledAt[r.storyID]; ok && !p.Available && !r.at.After(at) {
			continue // superseded: the backfill recovered what this unavailable row could not read
		}
		agg, ok := byStory[r.storyID]
		if !ok {
			agg = &storyAgg{available: true}
			byStory[r.storyID] = agg
			order = append(order, r.storyID)
		}
		agg.seen = true
		if p.Available {
			agg.sum.FreshInput += p.FreshInput
			agg.sum.CacheRead += p.CacheRead
			agg.sum.CacheWrite += p.CacheWrite
			agg.sum.Output += p.Output
			agg.sum.CostUSD = addCostPtr(agg.sum.CostUSD, p.CostUSD)
			agg.backfilled = agg.backfilled || p.Backfilled
		} else if agg.available {
			agg.available = false
			agg.unavailableReason = p.UnavailableReason
		}
	}

	var sum driverCumulative
	anyUnavailable := !recon.TotalAvailable
	for _, storyID := range order {
		agg := byStory[storyID]
		row := SessionReconciliationStory{StoryID: storyID, Available: agg.available, Backfilled: agg.backfilled}
		if agg.available {
			row.FreshInput, row.CacheRead, row.CacheWrite, row.Output = agg.sum.FreshInput, agg.sum.CacheRead, agg.sum.CacheWrite, agg.sum.Output
			row.CostUSD = agg.sum.CostUSD
			sum.FreshInput += agg.sum.FreshInput
			sum.CacheRead += agg.sum.CacheRead
			sum.CacheWrite += agg.sum.CacheWrite
			sum.Output += agg.sum.Output
			sum.CostUSD = addCostPtr(sum.CostUSD, agg.sum.CostUSD)
		} else {
			row.UnavailableReason = agg.unavailableReason
			anyUnavailable = true
		}
		recon.Stories = append(recon.Stories, row)
	}

	if anyUnavailable {
		recon.UnattributedPartial = true
		return recon, nil
	}
	recon.UnattributedAvailable = true
	recon.Unattributed = driverCumulative{
		FreshInput: recon.Total.FreshInput - sum.FreshInput,
		CacheRead:  recon.Total.CacheRead - sum.CacheRead,
		CacheWrite: recon.Total.CacheWrite - sum.CacheWrite,
		Output:     recon.Total.Output - sum.Output,
	}
	if recon.Total.CostUSD != nil {
		v := *recon.Total.CostUSD
		if sum.CostUSD != nil {
			v -= *sum.CostUSD
		}
		recon.Unattributed.CostUSD = &v
	}
	return recon, nil
}

// addCostPtr adds b into a, allocating a fresh pointer when a is nil and b is
// not — nil+nil stays nil (cost stays unavailable, never a fabricated zero).
func addCostPtr(a, b *float64) *float64 {
	if b == nil {
		return a
	}
	v := *b
	if a != nil {
		v += *a
	}
	return &v
}

func appendDriverUsageRow(ctx context.Context, itemID string, payload DriverUsagePayload, now time.Time) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: itemID, Kind: ledger.KindDriverUsage, Actor: "executor", Payload: body,
	}, now)
}
