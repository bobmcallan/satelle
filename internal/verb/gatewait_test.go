package verb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func gateSnap(turns, calls int, unflushed bool) agentcli.DriverSnapshot {
	return agentcli.DriverSnapshot{
		Available: true, MayUndercountInFlightTurn: unflushed, Turns: turns, ModelCalls: calls,
		FreshInputTokens: 100 * turns, OutputTokens: 10 * turns,
	}
}

func gateCounted(t *testing.T, story string) costview.GateWait {
	t.Helper()
	sc, err := ComputeStoryCost(context.Background(), story)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.GateWaits) != 1 {
		t.Fatalf("gate waits = %+v, want exactly one", sc.GateWaits)
	}
	return sc.GateWaits[0]
}

// sty_c4b92c9e AC7: the count is READ FROM driver_usage rows, and the window is
// bounded by the harness's flush. On a harness that records a turn's usage only
// when the turn ends, the issue row and the delivery row — both read inside the
// turn that made the call — carry the same cumulative, so the wait is reported
// open (never zero) until a read that sees the next turn settles it.
func TestGateWaitRows_CountIsReadFromRowsAndBoundedByTheFlush(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-gw")
	t.Setenv("CLAUDECODE", "1")
	// issue, delivered and the first settle attempt all read turn 1 (10 calls so
	// far); then the turn flushes: turn 2 made 3 requests — issue, ack, consume.
	stubSnapshotter(t, gateSnap(1, 10, true), gateSnap(1, 10, true), gateSnap(1, 10, true), gateSnap(2, 13, true))
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	RecordGateWait(ctx, "sty_gw1", costview.GatePhaseIssue, "gw_x1", now)
	RecordGateWait(ctx, "sty_gw1", costview.GatePhaseDelivered, "gw_x1", now.Add(time.Minute))

	// Before the flush a story-cost read cannot close the wait, and writes nothing
	// that pretends to.
	SettleGateWaits(ctx, "sty_gw1", now.Add(2*time.Minute))
	if w := gateCounted(t, "sty_gw1"); !w.Open || w.ModelCalls != nil {
		t.Fatalf("before the flush the wait = %+v, want open with no count", w)
	}
	if rows := driverUsageRows(t, db, "sty_gw1"); len(rows) != 2 {
		t.Fatalf("an unflushed settle attempt wrote a row: %d rows", len(rows))
	}

	// After the flush the settle writes one row and the count is a difference of
	// two rows' cumulative counts, less the issuing request.
	SettleGateWaits(ctx, "sty_gw1", now.Add(3*time.Minute))
	w := gateCounted(t, "sty_gw1")
	if w.Open || w.ModelCalls == nil || *w.ModelCalls != 2 {
		t.Fatalf("after the flush the wait = %+v, want 2 driver model calls", w)
	}
	rows := driverUsageRows(t, db, "sty_gw1")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want issue + delivered + settled", len(rows))
	}
	for i, phase := range []string{costview.GatePhaseIssue, costview.GatePhaseDelivered, costview.GatePhaseSettled} {
		r := rows[i]
		if r.Trigger != DriverTriggerGate || r.From != phase || r.To != "gw_x1" || !r.Unflushed {
			t.Errorf("row %d = trigger %q from %q to %q unflushed=%v, want a gate %s row for gw_x1", i, r.Trigger, r.From, r.To, r.Unflushed, phase)
		}
		if r.Cumulative.ModelCalls == nil {
			t.Errorf("row %d carries no cumulative model_calls", i)
		}
	}
	if rows[2].ModelCalls == nil || *rows[2].ModelCalls != 3 || *rows[2].Cumulative.ModelCalls != 13 {
		t.Errorf("settled row model_calls = %v cumulative = %v, want a delta of 3 on 13", rows[2].ModelCalls, rows[2].Cumulative.ModelCalls)
	}
	// Settling again is idempotent: the wait stays counted, no second row.
	SettleGateWaits(ctx, "sty_gw1", now.Add(4*time.Minute))
	if rows := driverUsageRows(t, db, "sty_gw1"); len(rows) != 3 {
		t.Fatalf("a second settle wrote another row: %d", len(rows))
	}
}

// A harness that records live: the delivery row precedes the driver's consume, so
// a settle before any request after it leaves the wait open.
func TestGateWaitRows_LiveHarnessSettlesOnlyAfterARequestPastTheDelivery(t *testing.T) {
	wireDU(t)
	t.Setenv(config.SessionEnv, "sess-gw-live")
	t.Setenv("CLAUDECODE", "1")
	// issue: 20 (includes the issuing request); delivery read: 21 (the ack); a
	// settle before the consume still reads 21; then the consume: 22.
	stubSnapshotter(t, gateSnap(20, 20, false), gateSnap(21, 21, false), gateSnap(21, 21, false), gateSnap(22, 22, false))
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	RecordGateWait(ctx, "sty_gw2", costview.GatePhaseIssue, "gw_y1", now)
	RecordGateWait(ctx, "sty_gw2", costview.GatePhaseDelivered, "gw_y1", now.Add(time.Minute))
	SettleGateWaits(ctx, "sty_gw2", now.Add(2*time.Minute))
	if w := gateCounted(t, "sty_gw2"); !w.Open {
		t.Fatalf("a settle before the consume closed the wait: %+v", w)
	}
	SettleGateWaits(ctx, "sty_gw2", now.Add(3*time.Minute))
	w := gateCounted(t, "sty_gw2")
	if w.Open || w.ModelCalls == nil || *w.ModelCalls != 2 {
		t.Fatalf("wait = %+v, want 2 (ack + consume)", w)
	}
}

// A harness that cannot count says so on the row and in the wait, adapter-named.
func TestGateWaitRows_UnavailableCountIsRecordedNotZeroed(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-gw-codex")
	t.Setenv("CLAUDECODE", "1")
	uncounted := gateSnap(2, 0, true)
	uncounted.ModelCallsUnavailableReason = "codex: session rollout carries no model-call count"
	stubSnapshotter(t, uncounted)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	RecordGateWait(ctx, "sty_gw3", costview.GatePhaseIssue, "gw_z1", now)
	RecordGateWait(ctx, "sty_gw3", costview.GatePhaseDelivered, "gw_z1", now.Add(time.Minute))

	for _, r := range driverUsageRows(t, db, "sty_gw3") {
		if r.ModelCalls != nil || r.Cumulative.ModelCalls != nil {
			t.Errorf("an uncounted harness recorded a count: %+v", r)
		}
		if !strings.HasPrefix(r.ModelCallsUnavailableReason, "codex:") {
			t.Errorf("reason = %q, want the adapter-named reason on the row", r.ModelCallsUnavailableReason)
		}
	}
	w := gateCounted(t, "sty_gw3")
	if w.ModelCalls != nil || w.Open || !strings.HasPrefix(w.Reason, "codex:") {
		t.Fatalf("wait = %+v, want no count, not open, a codex-named reason", w)
	}
}

// Ordinary rows carry the model-call delta too, diffed like the token fields; a
// base row from before the count existed is reported, not subtracted from.
func TestRecordDriverUsage_CarriesModelCallDelta(t *testing.T) {
	db := wireDU(t)
	t.Setenv(config.SessionEnv, "sess-mc")
	t.Setenv("CLAUDECODE", "1")
	stubSnapshotter(t, gateSnap(5, 5, false), gateSnap(9, 9, false))
	item := workitem.Item{ID: "sty_mc1", Kind: workitem.KindStory, Status: "plan"}
	now := time.Unix(1_700_000_000, 0)

	recordDriverUsage(context.Background(), item, "backlog", "plan", now)
	recordDriverUsage(context.Background(), item, "plan", "in_progress", now.Add(time.Minute))

	rows := driverUsageRows(t, db, "sty_mc1")
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[1].ModelCalls == nil || *rows[1].ModelCalls != 4 || rows[1].Cumulative.ModelCalls == nil || *rows[1].Cumulative.ModelCalls != 9 {
		t.Fatalf("second row model_calls = %v cumulative = %v, want a delta of 4 on 9", rows[1].ModelCalls, rows[1].Cumulative.ModelCalls)
	}

	var p DriverUsagePayload
	stampModelCalls(&p, gateSnap(9, 9, false), driverCumulative{ModelCalls: intp(9)}, driverCumulative{})
	if p.ModelCalls != nil || p.ModelCallsUnavailableReason == "" {
		t.Fatalf("a base with no count produced %+v, want a reason and no delta", p)
	}
}

func intp(n int) *int { return &n }
