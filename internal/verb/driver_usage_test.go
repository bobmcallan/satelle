package verb

// Internal tests for the driver-usage recorder (sty_81caa41b). Covers AC1
// (payload shape), AC3 (per-session high-water mark isolates two stories),
// AC4 (unreadable record dedup), AC5 (mid-turn kill via reaped lease), AC6
// (late-become-readable usage caught up onto the story it belongs to), AC7
// (idempotent retry) and AC8's same-session-id resume case. AC8's
// forked-session-id resume case remains unimplemented — see the doc comment
// on recordDriverUsage in driver_usage.go for why (no adapter surfaces that
// lineage).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// plantedHoldersDead makes the pid probe say the process behind every seat these
// tests plant is gone. A planted lease is stamped with this test process's own
// (live) pid, and a live in-flight pid keeps a seat Alive whatever its
// heartbeat — so the kill these tests simulate needs the probe to agree.
func plantedHoldersDead(t *testing.T) {
	t.Helper()
	prev := lease.PidAlive
	lease.PidAlive = func(int) bool { return false }
	t.Cleanup(func() { lease.PidAlive = prev })
}

func wireDU(t *testing.T) *store.DB {
	t.Helper()
	plantedHoldersDead(t)
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	SetWorkItemStore(db.Stories)
	SetLedgerStore(db.Ledger)
	SetTxRunner(db.InTx)
	SetDocIndexStore(db.DocIndex)
	SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
		SetWorkItemStore(nil)
		SetLedgerStore(nil)
		SetTxRunner(nil)
		SetDocIndexStore(nil)
		SetLeaseStore(nil)
	})
	return db
}

// stubSnapshotter installs a fixed sequence of DriverSnapshots as
// driverSnapshotter, returning the last one for any call beyond the sequence,
// and restores the real reader on cleanup.
func stubSnapshotter(t *testing.T, seq ...agentcli.DriverSnapshot) {
	t.Helper()
	i := 0
	prev := driverSnapshotter
	driverSnapshotter = func(harness, sessionID, repoRoot string) agentcli.DriverSnapshot {
		if i >= len(seq) {
			return seq[len(seq)-1]
		}
		s := seq[i]
		i++
		return s
	}
	t.Cleanup(func() { driverSnapshotter = prev })
}

func driverUsageRows(t *testing.T, db *store.DB, storyID string) []DriverUsagePayload {
	t.Helper()
	entries, err := db.Ledger.ListByStory(context.Background(), storyID, ledger.KindDriverUsage)
	if err != nil {
		t.Fatalf("ListByStory: %v", err)
	}
	var rows []DriverUsagePayload
	for _, e := range entries {
		var p DriverUsagePayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("unmarshal driver_usage payload: %v", err)
		}
		rows = append(rows, p)
	}
	return rows
}

func cost(v float64) *float64 { return &v }

// TestRecordDriverUsageWritesDelta pins AC1: an available snapshot writes a
// row carrying the delta since the previous snapshot for the same session,
// plus the cumulative high-water mark.
func TestRecordDriverUsageWritesDelta(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-1")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 100, CacheReadInputTokens: 20, CacheCreationInputTokens: 10, OutputTokens: 30},
		agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 260, CacheReadInputTokens: 45, CacheCreationInputTokens: 10, OutputTokens: 70, CostUSD: cost(0.01)},
	)
	item := workitem.Item{ID: "sty_du1", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "plan", "in_progress", now.Add(time.Minute))

	rows := driverUsageRows(t, db, "sty_du1")
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows = %d, want 2: %+v", len(rows), rows)
	}
	second := rows[1]
	if !second.Available {
		t.Fatalf("second row not available: %+v", second)
	}
	if second.FreshInput != 160 || second.CacheRead != 25 || second.CacheWrite != 0 || second.Output != 40 {
		t.Fatalf("delta = %+v, want fresh=160 cacheRead=25 cacheWrite=0 out=40", second)
	}
	if second.CostUSD == nil || *second.CostUSD != 0.01 {
		t.Fatalf("cost delta = %v, want 0.01 (no prior cost to subtract)", second.CostUSD)
	}
	if second.SessionID != "sess-du-1" || second.Executable != agentcli.HarnessClaude {
		t.Fatalf("session/executable = %q/%q", second.SessionID, second.Executable)
	}
}

// TestRecordDriverUsageIdempotentOnRetry pins AC7: a retried transition (same
// window, cumulative unchanged) writes nothing further.
func TestRecordDriverUsageIdempotentOnRetry(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-2")
	t.Setenv("CLAUDECODE", "1")
	snap := agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 30}
	stubSnapshotter(t, snap, snap, snap)
	item := workitem.Item{ID: "sty_du2", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "backlog", "plan", now.Add(time.Second))
	recordDriverUsage(context.Background(), item, "backlog", "plan", now.Add(2*time.Second))

	rows := driverUsageRows(t, db, "sty_du2")
	if len(rows) != 1 {
		t.Fatalf("driver_usage rows = %d, want 1 (retry must not duplicate): %+v", len(rows), rows)
	}
}

// TestRecordDriverUsageUnavailableDedup pins AC4: an unreadable record
// snapshotted repeatedly with the SAME reason writes one row; a CHANGED reason
// writes a new one.
func TestRecordDriverUsageUnavailableDedup(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-3")
	t.Setenv("CLAUDECODE", "1")
	unavailA := agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript unreadable: no such file"}
	unavailB := agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript unreadable: permission denied"}
	stubSnapshotter(t, unavailA, unavailA, unavailB)
	item := workitem.Item{ID: "sty_du3", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "backlog", "plan", now.Add(time.Second))
	rows := driverUsageRows(t, db, "sty_du3")
	if len(rows) != 1 {
		t.Fatalf("driver_usage rows after 2 identical-reason snapshots = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].Available {
		t.Fatalf("row must be Available=false: %+v", rows[0])
	}

	recordDriverUsage(context.Background(), item, "backlog", "plan", now.Add(2*time.Second))
	rows = driverUsageRows(t, db, "sty_du3")
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows after reason change = %d, want 2: %+v", len(rows), rows)
	}
	if rows[1].UnavailableReason != unavailB.UnavailableReason {
		t.Fatalf("second row reason = %q, want %q", rows[1].UnavailableReason, unavailB.UnavailableReason)
	}
}

// TestRecordDriverUsageIsolatesConsecutiveStories pins AC3: two stories
// driven by the SAME session, one after the other, each attribute only their
// own window — the session-level high-water mark (not a per-story mark) is
// what keeps story B from inheriting story A's cumulative as its own
// baseline. Claude never sets MayUndercountInFlightTurn (its transcript
// already carries the calling message's own usage synchronously — see
// agentcli.DriverSnapshot's doc comment), so A's close row is never Pending
// and no AC6 catch-up fires: this is the plain, un-bounded per-session
// high-water-mark split, unaffected by the Pending mechanism.
func TestRecordDriverUsageIsolatesConsecutiveStories(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-shared")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 10}, // A engage
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 400, OutputTokens: 40}, // A close
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 500, OutputTokens: 50}, // B engage (small gap since A close)
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 900, OutputTokens: 90}, // B close
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_duA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_duB", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Minute))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Minute))
	recordDriverUsage(context.Background(), b, "plan", "done", now.Add(3*time.Minute))

	aRows := driverUsageRows(t, db, "sty_duA")
	bRows := driverUsageRows(t, db, "sty_duB")
	if len(aRows) != 2 || len(bRows) != 2 {
		t.Fatalf("row counts = A:%d B:%d, want 2/2 (claude never stamps Pending, no AC6 catch-up)", len(aRows), len(bRows))
	}
	if aRows[1].Pending {
		t.Fatalf("claude's close row must never be Pending: %+v", aRows[1])
	}
	// A's total window: 100 -> 400 = 300 fresh, 10 -> 40 = 30 out.
	if aRows[0].FreshInput+aRows[1].FreshInput != 300 || aRows[0].Output+aRows[1].Output != 30 {
		t.Fatalf("A totals = %+v / %+v, want fresh sum 300 out sum 30", aRows[0], aRows[1])
	}
	// B's window starts from the session mark A left (400), NOT from zero:
	// engage delta = 500-400=100, close delta = 900-500=400.
	if bRows[0].FreshInput != 100 {
		t.Fatalf("B engage delta = %d, want 100 (baseline must be A's last cumulative, not zero)", bRows[0].FreshInput)
	}
	if bRows[1].FreshInput != 400 {
		t.Fatalf("B close delta = %d, want 400", bRows[1].FreshInput)
	}
}

// TestRecordDriverUsagePendingCatchupBoundedToExactlyOneTurn pins the
// correction the reviewer named: an available-but-Pending close row (grok
// only) must NOT credit the closed story with everything the next read
// happens to see — only the usage the harness's own Turns counter proves is
// the ONE turn that was in flight at close time. Here the confirming read (B
// engaging) advances Turns by exactly 1: that turn's delta is swept onto A,
// and nothing more.
func TestRecordDriverUsagePendingCatchupBoundedToExactlyOneTurn(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-bounded")
	t.Setenv("GROK_AGENT", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 3}, // A engage
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 400, OutputTokens: 40, Turns: 5}, // A close: Turns=5, in-flight turn 6 not yet flushed
		// B engage: Turns=6 — TurnBreakdown[5] (index == A's close Turns) is the
		// one confirmed in-flight turn; indices 0-4 are unused by the catch-up
		// (it reads only TurnBreakdown[prev.Turns]) and are left zero-value.
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 480, OutputTokens: 48, Turns: 6,
			TurnBreakdown: append(make([]agentcli.DriverTurn, 5), agentcli.DriverTurn{FreshInput: 80, Output: 8})},
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_du_boundedA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_du_boundedB", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Minute))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Minute))

	aRows := driverUsageRows(t, db, "sty_du_boundedA")
	if len(aRows) != 3 {
		t.Fatalf("A rows = %d, want 3 (engage, pending close, bounded late catch-up): %+v", len(aRows), aRows)
	}
	if !aRows[1].Pending {
		t.Fatalf("A close row must be Pending (MayUndercountInFlightTurn harness): %+v", aRows[1])
	}
	late := aRows[2]
	if late.Trigger != DriverTriggerLate || late.FreshInput != 80 || late.Output != 8 {
		t.Fatalf("A late row = %+v, want Trigger=late fresh=80 out=8 (480-400, 48-40: exactly the one confirmed turn)", late)
	}

	bRows := driverUsageRows(t, db, "sty_du_boundedB")
	if len(bRows) != 1 || bRows[0].FreshInput != 0 {
		t.Fatalf("B engage row = %+v, want a single zero-delta row (the confirmed turn went to A, not B)", bRows)
	}
}

// TestRecordDriverUsagePendingCatchupCreditsOnlyClosingTurn pins the other
// half of the same correction: when the confirming read shows MORE than one
// new turn since the Pending close, only the FIRST of those new turns — at
// index prev.Turns in the harness's per-turn breakdown, the one that was
// still running when the close call was made — is credited to the closed
// story. Every later turn started only once that one had already finished,
// i.e. strictly after the close, so it is left uncredited to anyone rather
// than guessed at (AC3/AC9): it shows up only in the reconciliation's honest
// "unattributed" remainder, and the current story's own row still starts
// clean (not stealing any of that remainder either).
func TestRecordDriverUsagePendingCatchupCreditsOnlyClosingTurn(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-multiturn")
	t.Setenv("GROK_AGENT", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 3}, // A engage
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 400, OutputTokens: 40, Turns: 5}, // A close: Turns=5
		// B engage MUCH later: Turns=9 — 4 new turns since the close, not just
		// the 1 that was in flight at close time. TurnBreakdown[5] (index ==
		// A's close Turns) is that one in-flight turn; indices 6-8 are three
		// further, unrelated turns that started only after the close.
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 900, OutputTokens: 90, Turns: 9,
			TurnBreakdown: append(append(make([]agentcli.DriverTurn, 5), agentcli.DriverTurn{FreshInput: 80, Output: 8}),
				agentcli.DriverTurn{FreshInput: 140, Output: 14}, agentcli.DriverTurn{FreshInput: 140, Output: 14}, agentcli.DriverTurn{FreshInput: 140, Output: 14})},
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_du_multiA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_du_multiB", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Minute))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(time.Hour))

	aRows := driverUsageRows(t, db, "sty_du_multiA")
	if len(aRows) != 3 {
		t.Fatalf("A rows = %d, want 3 (engage, pending close, closing-turn catch-up): %+v", len(aRows), aRows)
	}
	if !aRows[1].Pending {
		t.Fatalf("A close row must remain Pending on the append-only ledger: %+v", aRows[1])
	}
	late := aRows[2]
	if late.Trigger != DriverTriggerLate || late.FreshInput != 80 || late.Output != 8 {
		t.Fatalf("A late row = %+v, want Trigger=late fresh=80 out=8 (exactly the one closing turn, none of the 3 later ones)", late)
	}

	bRows := driverUsageRows(t, db, "sty_du_multiB")
	if len(bRows) != 1 || bRows[0].FreshInput != 0 {
		t.Fatalf("B engage row = %+v, want a single zero-delta row (the later, unrelated turns must not land on B either)", bRows)
	}

	// recon.Total is the raw harness cumulative (900/90). Attributed sums:
	// A's own engage row seeds a zero-delta baseline (the FIRST-ever snapshot
	// for this session — its pre-baseline accrual is never anyone's delta),
	// its close row is 300/30, its late row is 80/8 (the one closing turn);
	// B's engage row is 0/0. Attributed total = 380/38, so the remainder —
	// the pre-baseline accrual plus the 3 later, unrelated turns neither A
	// nor B could safely claim (100 + 140*3 = 520, 10 + 14*3 = 52) — lands in
	// AC9's honest "unattributed" figure, not fabricated onto either story.
	recon, err := ComputeSessionReconciliation(context.Background(), "sess-du-multiturn")
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	if !recon.UnattributedAvailable || recon.UnattributedPartial {
		t.Fatalf("recon unattributed availability = %+v, want available and not partial", recon)
	}
	if recon.Unattributed.FreshInput != 520 || recon.Unattributed.Output != 52 {
		t.Fatalf("unattributed = %+v, want fresh=520 out=52 (900 total - 300 A-close - 80 A-late)", recon.Unattributed)
	}
}

// TestComputeSessionReconciliationSumsStoriesAgainstTotal pins AC9: two
// stories driven by the same session, plus a gap between A's close and B's
// engage (the same claude fixture as TestRecordDriverUsageIsolatesConsecutiveStories,
// where AC6's Pending catch-up never fires — claude never undercounts its
// own turn) — the reconciliation's per-story sums, total and remainder must
// all line up.
func TestComputeSessionReconciliationSumsStoriesAgainstTotal(t *testing.T) {
	wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-recon")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 10}, // A engage
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 400, OutputTokens: 40}, // A close
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 500, OutputTokens: 50}, // B engage
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 900, OutputTokens: 90}, // B close
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_reconA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_reconB", Kind: workitem.KindStory, Status: "plan"}
	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Minute))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Minute))
	recordDriverUsage(context.Background(), b, "plan", "done", now.Add(3*time.Minute))

	recon, err := ComputeSessionReconciliation(context.Background(), "sess-du-recon")
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	if !recon.TotalAvailable || recon.Total.FreshInput != 900 || recon.Total.Output != 90 {
		t.Fatalf("total = %+v, want available fresh=900 out=90", recon)
	}
	byStory := map[string]SessionReconciliationStory{}
	for _, s := range recon.Stories {
		byStory[s.StoryID] = s
	}
	if got := byStory["sty_reconA"]; !got.Available || got.FreshInput != 300 || got.Output != 30 {
		t.Fatalf("story A = %+v, want available fresh=300 out=30", got)
	}
	if got := byStory["sty_reconB"]; !got.Available || got.FreshInput != 500 || got.Output != 50 {
		t.Fatalf("story B = %+v, want available fresh=500 out=50", got)
	}
	if !recon.UnattributedAvailable || recon.UnattributedPartial {
		t.Fatalf("unattributed availability = %+v, want available and not partial", recon)
	}
	if recon.Unattributed.FreshInput != 100 || recon.Unattributed.Output != 10 {
		t.Fatalf("unattributed = %+v, want fresh=100 out=10 (A's own first-ever snapshot seeds its baseline rather than counting as a delta)", recon.Unattributed)
	}
}

// TestComputeSessionReconciliationPartialOnUnavailableStory pins AC9's
// unavailable handling: one story with an unavailable row marks the whole
// remainder partial rather than computing a falsely-precise number.
func TestComputeSessionReconciliationPartialOnUnavailableStory(t *testing.T) {
	wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-recon-partial")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 10},
		agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript unreadable: no such file"},
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_reconC", Kind: workitem.KindStory, Status: "plan"}
	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "blocked", now.Add(time.Minute))

	recon, err := ComputeSessionReconciliation(context.Background(), "sess-du-recon-partial")
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	if !recon.UnattributedPartial || recon.UnattributedAvailable {
		t.Fatalf("unattributed availability = %+v, want partial and not available", recon)
	}
	if len(recon.Stories) != 1 || recon.Stories[0].Available {
		t.Fatalf("stories = %+v, want one unavailable story", recon.Stories)
	}
}

// TestRecordDriverUsageNoSessionIsNoop: no session identity resolvable ⇒ no
// row at all, rather than a row attributed to an empty session id.
func TestRecordDriverUsageNoSessionIsNoop(t *testing.T) {
	db := wireDU(t)
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "")
	item := workitem.Item{ID: "sty_du_nosess", Kind: workitem.KindStory, Status: "plan"}
	recordDriverUsage(context.Background(), item, "backlog", "plan", time.Now())
	rows := driverUsageRows(t, db, "sty_du_nosess")
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 with no resolvable session", len(rows))
	}
}

// TestRecordDriverUsageResumeSameSessionIDAttributesOnlyNewUsage pins AC8's
// same-session-id resume case: a session that stops and later resumes under
// the SAME session id (the harness's own resume, not a fork to a new id)
// needs no special handling — the existing per-session high-water mark
// already attributes only the usage recorded after the last snapshot, resume
// or not, because the resumed session's on-disk record simply carries more
// history than before.
func TestRecordDriverUsageResumeSameSessionIDAttributesOnlyNewUsage(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-resume")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 10}, // pre-pause engage
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 260, OutputTokens: 34}, // post-resume close: the harness record now carries MORE history, same session id
	)
	item := workitem.Item{ID: "sty_du_resume", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "plan", "done", now.Add(2*time.Hour)) // an arbitrary gap: pause + resume, same session id

	rows := driverUsageRows(t, db, "sty_du_resume")
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows = %d, want 2: %+v", len(rows), rows)
	}
	if rows[1].FreshInput != 160 || rows[1].Output != 24 {
		t.Fatalf("post-resume delta = %+v, want fresh=160 out=24 — only usage AFTER the resume point, not the full 260/34 the harness record now carries", rows[1])
	}
}

// TestRecordDriverUsageLateCatchupAttributesToClosedStory pins AC6: story A's
// close snapshot fails to read (unavailable), so its ledger row carries no
// cumulative. When the SAME session next takes a successful snapshot — for a
// DIFFERENT story B — the usage that accrued in between is caught up as a
// Late row on A, the story it belongs to, not folded into B.
func TestRecordDriverUsageLateCatchupAttributesToClosedStory(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-late")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 100, OutputTokens: 10},                                   // A engage
		agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript unreadable: no such file"}, // A close: read fails
		agentcli.DriverSnapshot{Available: true, FreshInputTokens: 500, OutputTokens: 50},                                   // B engage: readable again
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_du_lateA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_du_lateB", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Minute))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Minute))

	aRows := driverUsageRows(t, db, "sty_du_lateA")
	if len(aRows) != 3 {
		t.Fatalf("A rows = %d, want 3 (engage, unavailable close, late catch-up): %+v", len(aRows), aRows)
	}
	late := aRows[2]
	if late.Trigger != DriverTriggerLate || !late.Late {
		t.Fatalf("late row = %+v, want Trigger=late Late=true", late)
	}
	if !late.Available || late.FreshInput != 400 || late.Output != 40 {
		t.Fatalf("late row delta = %+v, want available fresh=400 out=40 (500-100, 50-10: the gap since A's last AVAILABLE snapshot)", late)
	}

	bRows := driverUsageRows(t, db, "sty_du_lateB")
	if len(bRows) != 1 {
		t.Fatalf("B rows = %d, want 1: %+v", len(bRows), bRows)
	}
	if bRows[0].FreshInput != 0 || bRows[0].Output != 0 {
		t.Fatalf("B engage delta = %+v, want zero — the gap was already attributed to A by the late row, not to B", bRows[0])
	}
}

// TestRecordDriverUsageOnReapWritesKillRow pins AC5: a driving session found
// dead by story-seat-list's stale-lease reap gets a final driver_usage row
// carrying whatever usage the harness record already contains plus the wall
// time, attributed to the story its seat lease was for — the row a session
// killed mid-turn (crash, Ctrl-C, OOM) could never write for itself.
func TestRecordDriverUsageOnReapWritesKillRow(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_kill", Kind: workitem.KindStory, Status: "plan"}

	t.Setenv(config.SessionEnv, "sess-du-kill")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 100, OutputTokens: 10})
	recordDriverUsage(ctx, item, "backlog", "plan", time.Unix(1_700_000_000, 0))

	// Plant a stale story-seat lease for the same session/story — the process
	// was killed mid-turn: no park/close transition, so no driver_usage row
	// beyond the engage snapshot above ever gets written through the normal path.
	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	// The reap-time read: the harness record already has more usage than the
	// engage snapshot saw — "whatever usage that record already contains" (AC5).
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 175, OutputTokens: 22})

	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}

	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows = %d, want 2 (engage + kill): %+v", len(rows), rows)
	}
	kill := rows[1]
	if kill.Trigger != DriverTriggerKill {
		t.Fatalf("trigger = %q, want %q", kill.Trigger, DriverTriggerKill)
	}
	if !kill.Available || kill.FreshInput != 75 || kill.Output != 12 {
		t.Fatalf("kill row = %+v, want available delta fresh=75 out=12", kill)
	}
	if kill.WallSeconds <= 0 {
		t.Fatalf("kill row WallSeconds = %v, want > 0", kill.WallSeconds)
	}

	remaining, err := db.Leases.List(ctx)
	if err != nil {
		t.Fatalf("list leases: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("leases remaining after reap = %d, want 0 (dead lease must be reaped): %+v", len(remaining), remaining)
	}
}

// TestRecordDriverUsageOnReapNoPartialTurnIsUnavailable pins AC5's other
// branch: when the harness record has no partial turn to report at reap time
// (still Available=false), the row is adapter-named unavailable plus the wall
// time — never a fabricated token figure.
func TestRecordDriverUsageOnReapNoPartialTurnIsUnavailable(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_kill_noturn", Kind: workitem.KindStory, Status: "plan"}

	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill-noturn",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	stubSnapshotter(t, agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript carries no assistant usage yet"})

	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}

	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 1 {
		t.Fatalf("driver_usage rows = %d, want 1: %+v", len(rows), rows)
	}
	kill := rows[0]
	if kill.Available {
		t.Fatalf("kill row must be Available=false: %+v", kill)
	}
	if kill.FreshInput != 0 || kill.CacheRead != 0 || kill.CacheWrite != 0 || kill.Output != 0 {
		t.Fatalf("kill row token fields must stay zero, not invented: %+v", kill)
	}
	if kill.WallSeconds <= 0 {
		t.Fatalf("kill row WallSeconds = %v, want > 0", kill.WallSeconds)
	}
	if kill.UnavailableReason == "" {
		t.Fatalf("kill row must carry an adapter-named unavailable reason: %+v", kill)
	}
}

// TestRecordDriverUsageOnReapNoProgressSincePriorRowIsUnavailable pins the
// review-round-3 fix: a reap-time read that comes back Available=true but
// with NO progress past the story's own last available snapshot (the record
// simply has nothing new to report — the in-flight turn never got as far as
// its first token) must never be written as an available row of invented
// zeros. It is indistinguishable from "no partial turn to report" and must
// be written the same way (sty_81caa41b AC5).
func TestRecordDriverUsageOnReapNoProgressSincePriorRowIsUnavailable(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_kill_noprogress", Kind: workitem.KindStory, Status: "plan"}

	t.Setenv(config.SessionEnv, "sess-du-kill-noprogress")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 100, OutputTokens: 10})
	recordDriverUsage(ctx, item, "backlog", "plan", time.Unix(1_700_000_000, 0))

	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill-noprogress",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	// Reap-time read: Available=true, but the SAME cumulative the engage
	// snapshot already saw — no new usage at all, not even a partial turn.
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 100, OutputTokens: 10})

	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}

	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows = %d, want 2 (engage + kill): %+v", len(rows), rows)
	}
	kill := rows[1]
	if kill.Available {
		t.Fatalf("kill row must be Available=false, not an available row of invented zeros: %+v", kill)
	}
	if kill.FreshInput != 0 || kill.CacheRead != 0 || kill.CacheWrite != 0 || kill.Output != 0 {
		t.Fatalf("kill row token fields must stay zero: %+v", kill)
	}
	if kill.UnavailableReason == "" {
		t.Fatalf("kill row must carry an adapter-named unavailable reason: %+v", kill)
	}
}

// TestRecordDriverUsageOnReapWithNoPriorRowIsUnavailable covers the OTHER
// no-progress case: the reaped session never wrote a driver_usage row at all
// (it died before its first transition), so there is no baseline to diff
// against — the base is trivially the current cumulative, and the delta is
// therefore always zero regardless of what the record contains. That must
// also be written unavailable, never a fabricated available zero-row
// (sty_81caa41b AC5).
func TestRecordDriverUsageOnReapWithNoPriorRowIsUnavailable(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_kill_nopriorrow", Kind: workitem.KindStory, Status: "plan"}

	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill-nopriorrow",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 250, OutputTokens: 25})

	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}

	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 1 {
		t.Fatalf("driver_usage rows = %d, want 1: %+v", len(rows), rows)
	}
	kill := rows[0]
	if kill.Available {
		t.Fatalf("kill row must be Available=false, not an invented zero-delta available row: %+v", kill)
	}
	if kill.FreshInput != 0 || kill.CacheRead != 0 || kill.CacheWrite != 0 || kill.Output != 0 {
		t.Fatalf("kill row token fields must stay zero: %+v", kill)
	}
	if kill.UnavailableReason == "" {
		t.Fatalf("kill row must carry an adapter-named unavailable reason: %+v", kill)
	}
}

// TestRecordDriverUsageOnReapBaseUsesLatestAvailableRowAfterUnavailable pins
// the Revision 4 fix: when the session's LATEST driver_usage row is itself
// unavailable (a prior reap already wrote a no-partial-turn row for this same
// dead session), a SECOND reap's base must diff against the latest AVAILABLE
// row, not the current cumulative — real usage that accrued since that last
// known-good point must still be recorded, exactly the fix
// recordLateDriverUsageCatchup's lateBase already applies to the ordinary
// late-catchup path.
func TestRecordDriverUsageOnReapBaseUsesLatestAvailableRowAfterUnavailable(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_kill_afterunavail", Kind: workitem.KindStory, Status: "plan"}

	t.Setenv(config.SessionEnv, "sess-du-kill-afterunavail")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 100, OutputTokens: 10})
	recordDriverUsage(ctx, item, "backlog", "plan", time.Unix(1_700_000_000, 0))

	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill-afterunavail",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	// First reap: the record cannot be read at all — writes an unavailable row,
	// NOT diffed from the engage row's cumulative (100/10).
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: false, UnavailableReason: "claude: session transcript unreadable: no such file"})
	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list (first reap): %v", err)
	}
	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 2 || rows[1].Available {
		t.Fatalf("rows after first reap = %+v, want 2 with the second Available=false", rows)
	}

	// Re-plant a stale lease (the first reap removed it) and let the record
	// become readable again with genuine NEW usage past the engage row.
	_, out, _, err = db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "local@host:99999",
		State: "plan", StorySeat: true, SessionID: "sess-du-kill-afterunavail",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("re-plant dead lease: out=%v err=%v", out, err)
	}
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat (2nd): %v", err)
	}
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 250, OutputTokens: 30})
	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list (second reap): %v", err)
	}

	rows = driverUsageRows(t, db, item.ID)
	if len(rows) != 3 {
		t.Fatalf("rows after second reap = %d, want 3 (engage, unavailable kill, available kill): %+v", len(rows), rows)
	}
	kill := rows[2]
	if !kill.Available {
		t.Fatalf("second kill row must be Available=true: %+v", kill)
	}
	if kill.FreshInput != 150 || kill.Output != 20 {
		t.Fatalf("second kill row delta = %+v, want fresh=150 out=20 (250-100, 30-10: diffed against the last AVAILABLE row, not the current cumulative)", kill)
	}
}

// TestSessionDriverUsageStateScansPastPageBoundary is a regression test for
// the bug the reviewer flagged: the old lookup used
// ledgerStore.List(...,Limit:2000), which orders OLDEST-first — once total
// driver_usage rows (across every session) exceeded the cap, the newest rows
// (the ones a "latest" lookup needs) were silently dropped, corrupting every
// later baseline. sessionDriverUsageState now scans via ForEachKind, which
// pages internally with no upper bound. This test forces many small pages
// (ForEachKindPageSize=3) with the target session's true latest row sitting
// well past several page boundaries, interleaved with other sessions' noise,
// and asserts it is still found.
func TestSessionDriverUsageStateScansPastPageBoundary(t *testing.T) {
	db := wireDU(t)
	origPageSize := ledger.ForEachKindPageSize
	ledger.ForEachKindPageSize = 3
	t.Cleanup(func() { ledger.ForEachKindPageSize = origPageSize })

	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	appendRow := func(storyID, sessionID string, fresh int, at time.Time) {
		p := DriverUsagePayload{
			SessionID: sessionID, Executable: agentcli.HarnessClaude, Available: true,
			Cumulative: driverCumulative{FreshInput: fresh},
			WindowKey:  fmt.Sprintf("%s-%d", sessionID, fresh),
		}
		body, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
			StoryID: storyID, Kind: ledger.KindDriverUsage, Actor: "executor", Payload: body,
		}, at); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 20; i++ {
		appendRow("sty_other", fmt.Sprintf("sess-other-%d", i), i, now.Add(time.Duration(i)*time.Second))
	}
	appendRow("sty_target", "sess-target", 100, now.Add(21*time.Second))
	for i := 0; i < 20; i++ {
		appendRow("sty_other", fmt.Sprintf("sess-noise-%d", i), i, now.Add(time.Duration(22+i)*time.Second))
	}
	appendRow("sty_target", "sess-target", 999, now.Add(43*time.Second))

	latest, found, _, _ := sessionDriverUsageState(ctx, "sess-target")
	if !found {
		t.Fatalf("sessionDriverUsageState: session not found")
	}
	if latest.payload.Cumulative.FreshInput != 999 {
		t.Fatalf("latest.Cumulative.FreshInput = %d, want 999 (the true latest row, not one truncated off by a bounded oldest-first scan)", latest.payload.Cumulative.FreshInput)
	}
}

// TestSweepPendingDriverUsageSettlesTailWithNoFollowingTransition pins AC6's
// other named gap: "a session's tail after its last close is never swept onto
// the closed story." Story A closes (Pending read, possibly undercounting its
// own turn on grok) and the session NEVER drives another story — no
// transition will ever call back in to confirm it. sweepPendingDriverUsage is
// the standalone entry point (wired into story-seat-list) that confirms it
// anyway.
func TestSweepPendingDriverUsageSettlesTailWithNoFollowingTransition(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-sweep")
	t.Setenv("GROK_AGENT", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 2}, // engage
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 400, OutputTokens: 40, Turns: 4}, // close: Pending, may undercount turn 5
	)
	item := workitem.Item{ID: "sty_du_sweep", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)
	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "plan", "done", now.Add(time.Minute))

	rows := driverUsageRows(t, db, "sty_du_sweep")
	if len(rows) != 2 || !rows[1].Pending {
		t.Fatalf("rows before sweep = %+v, want 2 with close Pending", rows)
	}

	// No further transition ever happens for this session — instead, the
	// close's own turn finally flushes (Turns advances by exactly 1, proving
	// it is that one turn and nothing more), and a LATER sweep (e.g. from an
	// unrelated `satelle story seat` call) is what discovers it.
	// TurnBreakdown[4] (index == the close row's Turns) is the one confirmed
	// in-flight turn the sweep credits back to the story.
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 480, OutputTokens: 48, Turns: 5,
		TurnBreakdown: append(make([]agentcli.DriverTurn, 4), agentcli.DriverTurn{FreshInput: 80, Output: 8})})
	sweepPendingDriverUsage(context.Background(), now.Add(time.Hour))

	rows = driverUsageRows(t, db, "sty_du_sweep")
	if len(rows) != 3 {
		t.Fatalf("rows after sweep = %d, want 3 (engage, pending close, late catch-up): %+v", len(rows), rows)
	}
	late := rows[2]
	if late.Trigger != DriverTriggerLate || !late.Available || late.FreshInput != 80 || late.Output != 8 {
		t.Fatalf("swept late row = %+v, want Trigger=late fresh=80 out=8 (480-400, 48-40)", late)
	}

	// A repeat sweep with no further growth is a no-op: the session's latest
	// row (the late row just written) is no longer Pending.
	sweepPendingDriverUsage(context.Background(), now.Add(2*time.Hour))
	rows = driverUsageRows(t, db, "sty_du_sweep")
	if len(rows) != 3 {
		t.Fatalf("rows after repeat sweep = %d, want 3 (settled row is not Pending, so nothing more to sweep)", len(rows))
	}
}

// TestSweepCreditsSharedTurnToEarliestPendingRowOnly pins Rule T (sty_81caa41b
// Revision 4), scenario (a): two sibling stories, A and B, both transition
// (engage, then close) inside the SAME underlying harness turn — a
// fast-moving in-loop driver can close A and engage+close B before the
// harness ever flushes that turn, so BOTH their close rows are Pending at the
// SAME Turns index. When the turn finally completes, the sweep must credit it
// to exactly ONE story — the earliest-created Pending row (A) — and B must get
// no late row for it at all.
func TestSweepCreditsSharedTurnToEarliestPendingRowOnly(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-ruleT-shared")
	t.Setenv("GROK_AGENT", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // A engage
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // A close: Pending at Turns=0
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // B engage: same in-flight turn, A's catchup attempt can't resolve yet
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // B close: ALSO Pending at Turns=0
		// The confirming read, once the shared turn finally flushes: exactly ONE
		// new turn, index 0, holding everything both A's tail and B's whole
		// lifecycle contributed.
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 180, OutputTokens: 18, Turns: 1,
			TurnBreakdown: []agentcli.DriverTurn{{FreshInput: 80, Output: 8}}},
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_ruleT_A", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_ruleT_B", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Second))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Second))
	recordDriverUsage(context.Background(), b, "plan", "done", now.Add(3*time.Second))

	aRows := driverUsageRows(t, db, "sty_ruleT_A")
	bRows := driverUsageRows(t, db, "sty_ruleT_B")
	if len(aRows) != 2 || !aRows[1].Pending {
		t.Fatalf("A rows before sweep = %+v, want 2 with close Pending", aRows)
	}
	if len(bRows) != 2 || !bRows[1].Pending {
		t.Fatalf("B rows before sweep = %+v, want 2 with close ALSO Pending (same shared turn)", bRows)
	}

	sweepPendingDriverUsage(context.Background(), now.Add(time.Hour))

	aRows = driverUsageRows(t, db, "sty_ruleT_A")
	bRows = driverUsageRows(t, db, "sty_ruleT_B")
	if len(aRows) != 3 {
		t.Fatalf("A rows after sweep = %d, want 3 (engage, pending close, late credit): %+v", len(aRows), aRows)
	}
	late := aRows[2]
	if late.Trigger != DriverTriggerLate || late.FreshInput != 80 || late.Output != 8 {
		t.Fatalf("A late row = %+v, want Trigger=late fresh=80 out=8", late)
	}
	if len(late.CreditedTurns) != 1 || late.CreditedTurns[0] != 0 {
		t.Fatalf("A late row CreditedTurns = %v, want [0]", late.CreditedTurns)
	}
	if len(bRows) != 2 {
		t.Fatalf("B rows after sweep = %d, want STILL 2 (no late row — the shared turn already went to A, the earliest Pending row): %+v", len(bRows), bRows)
	}

	recon, err := ComputeSessionReconciliation(context.Background(), "sess-du-ruleT-shared")
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	if !recon.TotalAvailable {
		t.Fatalf("recon total must be available: %+v", recon)
	}
	var sumFresh, sumOut int
	for _, s := range recon.Stories {
		if !s.Available {
			t.Fatalf("story %s unexpectedly unavailable: %+v", s.StoryID, s)
		}
		sumFresh += s.FreshInput
		sumOut += s.Output
	}
	if sumFresh > recon.Total.FreshInput || sumOut > recon.Total.Output {
		t.Fatalf("attributed sum (fresh=%d out=%d) exceeds harness total %+v — the shared turn was double-counted", sumFresh, sumOut, recon.Total)
	}
	if recon.Unattributed.FreshInput < 0 || recon.Unattributed.Output < 0 {
		t.Fatalf("unattributed remainder must never be negative: %+v", recon.Unattributed)
	}
}

// TestOrdinaryDeltaAndLateCreditNeverBothClaimSameTurn pins Rule T
// (sty_81caa41b Revision 4), scenario (b): A closes Pending at Turns=N; B
// engages inside that same in-flight turn (so A's inline catch-up attempt
// cannot resolve yet); the turn only completes on B's OWN next (ordinary,
// non-Pending) transition, so B's plain delta computation is the first thing
// to observe it. A LATER sweep must then find that turn's index already
// consumed by B's ordinary row and refuse to also credit it to A as a late
// row — the turn is counted exactly once.
func TestOrdinaryDeltaAndLateCreditNeverBothClaimSameTurn(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-du-ruleT-ordinary")
	t.Setenv("GROK_AGENT", "1")
	stubSnapshotter(t,
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // A engage
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // A close: Pending at Turns=0
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 100, OutputTokens: 10, Turns: 0}, // B engage: same in-flight turn
		// B's own NEXT transition: the shared turn has now completed. This is an
		// ORDINARY (non-Close/Park) transition, so it is never itself Pending —
		// it computes its plain delta first, before any sweep ever runs.
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 180, OutputTokens: 18, Turns: 1,
			TurnBreakdown: []agentcli.DriverTurn{{FreshInput: 80, Output: 8}}},
		// The sweep's later re-read of the same, unchanged state.
		agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, FreshInputTokens: 180, OutputTokens: 18, Turns: 1,
			TurnBreakdown: []agentcli.DriverTurn{{FreshInput: 80, Output: 8}}},
	)
	now := time.Unix(1_700_000_000, 0)
	a := workitem.Item{ID: "sty_ruleT_ordA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_ruleT_ordB", Kind: workitem.KindStory, Status: "plan"}

	recordDriverUsage(context.Background(), a, "backlog", "plan", now)
	recordDriverUsage(context.Background(), a, "plan", "done", now.Add(time.Second))
	recordDriverUsage(context.Background(), b, "backlog", "plan", now.Add(2*time.Second))
	recordDriverUsage(context.Background(), b, "plan", "in_progress", now.Add(3*time.Second))

	bRows := driverUsageRows(t, db, "sty_ruleT_ordB")
	if len(bRows) != 2 {
		t.Fatalf("B rows = %d, want 2 (engage, ordinary transition): %+v", len(bRows), bRows)
	}
	ordinary := bRows[1]
	if ordinary.FreshInput != 80 || ordinary.Output != 8 {
		t.Fatalf("B ordinary transition delta = %+v, want fresh=80 out=8 (the shared turn's full contribution, claimed before any sweep runs)", ordinary)
	}

	// A LATER sweep must find the turn already consumed by B's ordinary row and
	// refuse to credit it again to A.
	sweepPendingDriverUsage(context.Background(), now.Add(time.Hour))

	aRows := driverUsageRows(t, db, "sty_ruleT_ordA")
	if len(aRows) != 2 {
		t.Fatalf("A rows after sweep = %d, want STILL 2 (no late row — B's ordinary delta already claimed the shared turn): %+v", len(aRows), aRows)
	}

	recon, err := ComputeSessionReconciliation(context.Background(), "sess-du-ruleT-ordinary")
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	var sumFresh, sumOut int
	for _, s := range recon.Stories {
		sumFresh += s.FreshInput
		sumOut += s.Output
	}
	if sumFresh > recon.Total.FreshInput || sumOut > recon.Total.Output {
		t.Fatalf("attributed sum (fresh=%d out=%d) exceeds harness total %+v — the shared turn was double-counted", sumFresh, sumOut, recon.Total)
	}
	if recon.Unattributed.FreshInput < 0 || recon.Unattributed.Output < 0 {
		t.Fatalf("unattributed remainder must never be negative: %+v", recon.Unattributed)
	}
}

// wireDUWithEngagingWorkflow is wireDU plus a minimal DOT workflow synced
// into DocIndex, so storyStatusIsEngaging (and therefore
// acquireEngagementLease's story-seat path) resolves instead of falling back
// to "no governing workflow". Duplicates the shape of the verb_test package's
// singleStoryWF/wireWithWorkflowsStore fixture (internal/verb/single_story_test.go
// via definition_freeze_test.go) rather than importing it — this file is
// package verb (internal), and acquireEngagementLease is unexported.
func wireDUWithEngagingWorkflow(t *testing.T) *store.DB {
	t.Helper()
	return wireDUWithWorkflow(t,
		"[\"*\"]\nobligations = [\"raised\", \"planned\"]\n",
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n[planned]\nstatus = \"plan\"\nagent = \"executor\"\nrequires = [\"raised\"]\n")
}

// wireDUWithWorkflow is wireDU plus a two-file fixture workflow (the "done" obligations
// table and the "step" states) synced into DocIndex.
func wireDUWithWorkflow(t *testing.T, doneTable, stepStates string) *store.DB {
	t.Helper()
	plantedHoldersDead(t)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const meta = "[meta]\nname = \"%s\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n"
	doneBody := fmt.Sprintf(meta, "done") + doneTable
	stepBody := fmt.Sprintf(meta, "step") + stepStates
	if err := os.WriteFile(filepath.Join(wfDir, "done.toml"), []byte(doneBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "step.toml"), []byte(stepBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatal(err)
	}
	SetWorkItemStore(db.Stories)
	SetLedgerStore(db.Ledger)
	SetTxRunner(db.InTx)
	SetDocIndexStore(db.DocIndex)
	SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
		SetWorkItemStore(nil)
		SetLedgerStore(nil)
		SetTxRunner(nil)
		SetDocIndexStore(nil)
		SetLeaseStore(nil)
	})
	return db
}

// TestAcquireEngagementLeaseRecordsKillRowOnSteal pins the OTHER AC5 wiring
// point named by the review-round-3 fix: acquireEngagementLease's OWN steal
// path (single_story.go) — the one every real `satelle story set` call goes
// through — must record the replaced holder's final usage itself, not rely
// on story-seat-list's Reap sweep ever running first. Without this, a
// session that steals a stale seat before anyone runs `satelle story seat`
// would silently drop the dead holder's tail forever.
func TestAcquireEngagementLeaseRecordsKillRowOnSteal(t *testing.T) {
	db := wireDUWithEngagingWorkflow(t)
	ctx := context.Background()
	item := workitem.Item{ID: "sty_du_steal", Kind: workitem.KindStory, Status: "backlog"}

	// Plant a dead holder: a different owner/session already holds the seat,
	// but its heartbeat is stale. Its own engage snapshot establishes the
	// baseline the steal's reap-time read diffs against.
	t.Setenv(config.SessionEnv, "sess-du-steal")
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 60, OutputTokens: 6})
	recordDriverUsage(ctx, item, "backlog", "plan", time.Unix(1_700_000_000, 0))
	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: item.ID, Kind: "story", Owner: "dead-owner", State: "plan",
		StorySeat: true, SessionID: "sess-du-steal",
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant dead lease: out=%v err=%v", out, err)
	}
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, item.ID, staleAt); err != nil {
		t.Fatalf("freeze heartbeat: %v", err)
	}

	// The stealing call's own reap-time read of the dead holder's session.
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-sonnet-5", FreshInputTokens: 90, OutputTokens: 9})

	acquired, alreadyInFlight, err := acquireEngagementLease(ctx, item, "plan")
	if err != nil {
		t.Fatalf("acquireEngagementLease: %v", err)
	}
	if !acquired || alreadyInFlight {
		t.Fatalf("acquired=%v alreadyInFlight=%v, want acquired=true alreadyInFlight=false (steal)", acquired, alreadyInFlight)
	}

	rows := driverUsageRows(t, db, item.ID)
	if len(rows) != 2 {
		t.Fatalf("driver_usage rows = %d, want 2 (the dead holder's own engage row + its kill row): %+v", len(rows), rows)
	}
	kill := rows[1]
	if kill.Trigger != DriverTriggerKill {
		t.Fatalf("trigger = %q, want %q", kill.Trigger, DriverTriggerKill)
	}
	if !kill.Available || kill.FreshInput != 30 || kill.Output != 3 {
		t.Fatalf("kill row = %+v, want available delta fresh=30 out=3 (90-60, 9-6)", kill)
	}
}

// fakeTurnSession simulates a grok-style session record whose turns are
// appended one at a time by a test driving a SEQUENCE of actions
// (sty_81caa41b Revision 5's property test) — the per-turn breakdown every
// delta-carrying writer (ordinary, the single-turn Pending catch-up, the
// whole-gap Late catch-up, and the Kill row) now claims against through the
// one shared sessionClaimedTurns check.
type fakeTurnSession struct {
	turns       []agentcli.DriverTurn
	unavailable string
}

func (f *fakeTurnSession) snapshot() agentcli.DriverSnapshot {
	if f.unavailable != "" {
		return agentcli.DriverSnapshot{Available: false, UnavailableReason: f.unavailable}
	}
	snap := agentcli.DriverSnapshot{Available: true, MayUndercountInFlightTurn: true, Turns: len(f.turns)}
	snap.TurnBreakdown = append([]agentcli.DriverTurn(nil), f.turns...)
	for _, t := range f.turns {
		snap.FreshInputTokens += t.FreshInput
		snap.OutputTokens += t.Output
	}
	return snap
}

func (f *fakeTurnSession) flushTurn(fresh, out int) {
	f.turns = append(f.turns, agentcli.DriverTurn{FreshInput: fresh, Output: out})
}

// stubSessionSnapshotter wires driverSnapshotter to a set of named fake
// sessions keyed by session id — unlike stubSnapshotter's fixed reply
// sequence, each session's snapshot reflects its OWN mutable state at call
// time, which a multi-step sequence test needs (a step can flush a turn or
// flip availability between two calls into the SAME session).
func stubSessionSnapshotter(t *testing.T, sessions map[string]*fakeTurnSession) {
	t.Helper()
	prev := driverSnapshotter
	driverSnapshotter = func(harness, sessionID, repoRoot string) agentcli.DriverSnapshot {
		if s, ok := sessions[sessionID]; ok {
			snap := s.snapshot()
			snap.SessionID = sessionID
			return snap
		}
		return agentcli.DriverSnapshot{UnavailableReason: "test: no fake session wired for " + sessionID}
	}
	t.Cleanup(func() { driverSnapshotter = prev })
}

// assertTurnCreditInvariant is the Revision 5 property check, run after every
// step of a sequence test — not just at the end — so a violation is pinned to
// the exact step that caused it:
//
//	(a) no session turn index is counted by more than one AVAILABLE
//	    driver_usage row's own [BaseTurns,Turns) range, anywhere in the
//	    ledger, regardless of which story it landed on or what kind of row
//	    (ordinary, Pending, Late, Kill) claimed it;
//	(b) the sum attributed to every story never exceeds the harness's own
//	    latest known cumulative;
//	(c) the reconciliation remainder is never negative.
func assertTurnCreditInvariant(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	counts := map[int]int{}
	if err := ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || p.SessionID != sessionID || !p.Available {
			return nil
		}
		for idx := p.BaseTurns; idx < p.Turns; idx++ {
			counts[idx]++
		}
		return nil
	}); err != nil {
		t.Fatalf("ForEachKind: %v", err)
	}
	for idx, n := range counts {
		if n > 1 {
			t.Fatalf("(a) turn index %d counted by %d available rows, want at most 1", idx, n)
		}
	}

	recon, err := ComputeSessionReconciliation(ctx, sessionID)
	if err != nil {
		t.Fatalf("ComputeSessionReconciliation: %v", err)
	}
	var sumFresh, sumOut int
	for _, s := range recon.Stories {
		if s.Available {
			sumFresh += s.FreshInput
			sumOut += s.Output
		}
	}
	if recon.TotalAvailable && (sumFresh > recon.Total.FreshInput || sumOut > recon.Total.Output) {
		t.Fatalf("(b) attributed sum (fresh=%d out=%d) exceeds harness total %+v", sumFresh, sumOut, recon.Total)
	}
	if recon.UnattributedAvailable && (recon.Unattributed.FreshInput < 0 || recon.Unattributed.Output < 0) {
		t.Fatalf("(c) unattributed remainder negative: %+v", recon.Unattributed)
	}
}

// TestTurnCreditPropertySequence_ReapClaimsSharedTurnBeforeSweepCanDoubleCredit
// is Revision 5's mandatory sequence (i): A closes Pending at turn index N; B
// engages inside that SAME still-open turn (so A's own inline catch-up cannot
// resolve yet — the harness has nothing past index N to read); the turn then
// flushes; B's own seat is found stale and reaped by story-seat-list BEFORE
// anything resolves A's Pending row. Reap runs its kill-row recorder before
// its own trailing sweep (seat.go: recordDriverUsageOnReap, then
// sweepPendingDriverUsage) — the kill row is the FIRST writer to observe the
// flushed turn and claims it; the trailing sweep, which would otherwise
// credit A's still-open Pending row for the very same turn, must find it
// already claimed and back off. The turn is counted exactly once — the
// invariant holds after every step, not just at the end.
func TestTurnCreditPropertySequence_ReapClaimsSharedTurnBeforeSweepCanDoubleCredit(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	sessionID := "sess-prop-reap-then-sweep"
	fake := &fakeTurnSession{}
	stubSessionSnapshotter(t, map[string]*fakeTurnSession{sessionID: fake})
	t.Setenv(config.SessionEnv, sessionID)
	t.Setenv("GROK_AGENT", "1")

	a := workitem.Item{ID: "sty_prop_reapA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_prop_reapB", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(ctx, a, "backlog", "plan", now)
	assertTurnCreditInvariant(t, ctx, sessionID)
	recordDriverUsage(ctx, a, "plan", "done", now.Add(time.Second))
	assertTurnCreditInvariant(t, ctx, sessionID)
	aRows := driverUsageRows(t, db, a.ID)
	if len(aRows) != 2 || !aRows[1].Pending {
		t.Fatalf("A rows = %+v, want engage + Pending close (turn still open)", aRows)
	}

	// B engages inside the same still-open turn — the harness has not
	// flushed it yet, so B's own engage row is a clean zero-delta baseline.
	_, out, _, err := db.Leases.AcquireWith(ctx, lease.AcquireOpts{
		ItemID: b.ID, Kind: "story", Owner: "local@host:prop", State: "plan",
		StorySeat: true, SessionID: sessionID,
	})
	if err != nil || out != lease.OutcomeAcquired {
		t.Fatalf("plant B's lease: out=%v err=%v", out, err)
	}
	recordDriverUsage(ctx, b, "backlog", "plan", now.Add(2*time.Second))
	assertTurnCreditInvariant(t, ctx, sessionID)

	// The turn finally flushes — no writer has looked at it yet.
	fake.flushTurn(80, 8)
	assertTurnCreditInvariant(t, ctx, sessionID)

	// B's seat goes stale and gets reaped by story-seat-list.
	staleAt := time.Now().UTC().Add(-lease.HeartbeatTTL - time.Minute)
	if err := db.Leases.SetHeartbeat(ctx, b.ID, staleAt); err != nil {
		t.Fatalf("freeze B's heartbeat: %v", err)
	}
	if _, err := storySeatList(ctx, nil); err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}
	assertTurnCreditInvariant(t, ctx, sessionID)

	bRows := driverUsageRows(t, db, b.ID)
	if len(bRows) != 2 || bRows[1].Trigger != DriverTriggerKill || !bRows[1].Available {
		t.Fatalf("B rows = %+v, want engage + an available kill row", bRows)
	}
	if bRows[1].FreshInput != 80 || bRows[1].Output != 8 {
		t.Fatalf("B kill row = %+v, want fresh=80 out=8 (the flushed turn)", bRows[1])
	}

	aRows = driverUsageRows(t, db, a.ID)
	if len(aRows) != 2 {
		t.Fatalf("A rows after reap+sweep = %d, want STILL 2 (the turn was already claimed by B's kill row; A's Pending row stays open, uncredited): %+v", len(aRows), aRows)
	}
}

// TestTurnCreditPropertySequence_GapLateClaimsSharedTurnBeforeSweepCanDoubleCredit
// is Revision 5's mandatory sequence (ii): A closes Pending at turn index N;
// B's OWN engage read fails outright (Available=false); the shared turn then
// flushes and the record becomes readable again; B's NEXT transition is what
// discovers this — it writes a whole-gap Late row (recordLateDriverUsageCatchup,
// boundToSingleTurn=false) onto ITS OWN unavailable engage row before
// computing its own clean, forward-only delta. A later sweep must then find
// the shared turn already claimed by B's gap-late row and refuse to also
// credit it to A's still-open Pending row.
func TestTurnCreditPropertySequence_GapLateClaimsSharedTurnBeforeSweepCanDoubleCredit(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	sessionID := "sess-prop-gaplate-then-sweep"
	fake := &fakeTurnSession{}
	stubSessionSnapshotter(t, map[string]*fakeTurnSession{sessionID: fake})
	t.Setenv(config.SessionEnv, sessionID)
	t.Setenv("GROK_AGENT", "1")

	a := workitem.Item{ID: "sty_prop_gaplateA", Kind: workitem.KindStory, Status: "plan"}
	b := workitem.Item{ID: "sty_prop_gaplateB", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(ctx, a, "backlog", "plan", now)
	assertTurnCreditInvariant(t, ctx, sessionID)
	recordDriverUsage(ctx, a, "plan", "done", now.Add(time.Second))
	assertTurnCreditInvariant(t, ctx, sessionID)
	aRows := driverUsageRows(t, db, a.ID)
	if len(aRows) != 2 || !aRows[1].Pending {
		t.Fatalf("A rows = %+v, want engage + Pending close (turn still open)", aRows)
	}

	// B's own engage read fails outright.
	fake.unavailable = "test: session temporarily unreadable"
	recordDriverUsage(ctx, b, "backlog", "plan", now.Add(2*time.Second))
	assertTurnCreditInvariant(t, ctx, sessionID)
	bRows := driverUsageRows(t, db, b.ID)
	if len(bRows) != 1 || bRows[0].Available {
		t.Fatalf("B rows after unreadable engage = %+v, want 1 unavailable row", bRows)
	}

	// The shared turn flushes and the record becomes readable again — no
	// writer has looked at it yet.
	fake.flushTurn(80, 8)
	fake.unavailable = ""
	assertTurnCreditInvariant(t, ctx, sessionID)

	// B's OWN next transition discovers the record is readable again: it
	// writes a gap-late row attributing the whole gap (including the flushed
	// turn) onto its own unavailable engage row, before computing its own
	// clean, forward-only delta.
	recordDriverUsage(ctx, b, "plan", "in_progress", now.Add(time.Hour))
	assertTurnCreditInvariant(t, ctx, sessionID)

	bRows = driverUsageRows(t, db, b.ID)
	if len(bRows) != 3 {
		t.Fatalf("B rows = %d, want 3 (unavailable engage, gap-late catch-up, ordinary transition): %+v", len(bRows), bRows)
	}
	// The gap-late row and B's own transition row share a timestamp, so their
	// ledger order is not fixed; pick each by what it is, not by position.
	var gapLate, own *DriverUsagePayload
	for i := range bRows[1:] {
		r := &bRows[1+i]
		if r.Late {
			gapLate = r
		} else {
			own = r
		}
	}
	if gapLate == nil || own == nil {
		t.Fatalf("B rows = %+v, want one late and one ordinary row after the unavailable engage", bRows)
	}
	if gapLate.Trigger != DriverTriggerLate || gapLate.FreshInput != 80 || gapLate.Output != 8 {
		t.Fatalf("B gap-late row = %+v, want Trigger=late fresh=80 out=8 (the flushed turn)", *gapLate)
	}
	if own.FreshInput != 0 || own.Output != 0 {
		t.Fatalf("B's own transition delta = %+v, want zero (the gap was already claimed by its own gap-late row)", *own)
	}

	// A LATER sweep must find the turn already claimed by B's gap-late row
	// and refuse to also credit it to A's still-open Pending row.
	sweepPendingDriverUsage(ctx, now.Add(2*time.Hour))
	assertTurnCreditInvariant(t, ctx, sessionID)

	aRows = driverUsageRows(t, db, a.ID)
	if len(aRows) != 2 {
		t.Fatalf("A rows after sweep = %d, want STILL 2 (the turn was already claimed by B's gap-late row): %+v", len(aRows), aRows)
	}
}
