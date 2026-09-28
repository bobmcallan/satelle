package costview

import "testing"

func ip(n int) *int { return &n }

// gateRow builds a driver row the way the recorder writes one.
func gateRow(phase, handle string, calls *int, turns int, unflushed bool) DriverRow {
	r := DriverRow{
		SessionID: "sess-1", Executable: "grok", Available: true,
		Trigger: GateTrigger, From: phase, To: handle, Turns: turns, Unflushed: unflushed,
	}
	r.Cumulative.ModelCalls = calls
	return r
}

func plainRow(calls *int, turns int) DriverRow {
	r := DriverRow{SessionID: "sess-1", Executable: "grok", Available: true, Trigger: "transition", Turns: turns}
	r.Cumulative.ModelCalls = calls
	return r
}

// The flush-lag bound (sty_c4b92c9e): grok records a turn's usage only when the
// turn ends, so the issue row and the delivery row — both read inside the turn
// that made the call — show the SAME cumulative, and a count taken from them is
// zero whatever the driver did. The wait is therefore reported open until a row
// reads a later turn, and only then is it a count: the later cumulative minus
// the issue row's, less the one request that issued the command.
func TestGateWaits_FlushLagHarnessStaysOpenUntilTheTurnIsRead(t *testing.T) {
	rows := []DriverRow{
		gateRow(GatePhaseIssue, "gw_a", ip(10), 1, true),
		gateRow(GatePhaseDelivered, "gw_a", ip(10), 1, true), // same cumulative: not yet flushed
	}
	got := GateWaits(rows)
	if len(got) != 1 || !got[0].Open || got[0].ModelCalls != nil {
		t.Fatalf("before the flush: %+v, want one open wait with no count", got)
	}
	if FormatModelCalls(got[0]) != "open" {
		t.Fatalf("an open wait must render open, got %q", FormatModelCalls(got[0]))
	}

	// A row from the same turn (turn count unchanged) still cannot close it.
	rows = append(rows, plainRow(ip(10), 1))
	if got = GateWaits(rows); !got[0].Open {
		t.Fatalf("a read of the same turn closed the wait: %+v", got[0])
	}

	// The turn flushed: three requests (issue + ack + consume), two of them the wait.
	rows = append(rows, plainRow(ip(13), 2))
	got = GateWaits(rows)
	if got[0].Open || got[0].ModelCalls == nil || *got[0].ModelCalls != 2 {
		t.Fatalf("after the flush: %+v, want 2 driver model calls", got[0])
	}
}

// A harness that records live has the issuing request already in the issue row;
// its delivery row is taken before the driver consumes the verdict, so only a
// row with a request after the delivery closes the wait.
func TestGateWaits_LiveHarnessNeedsARequestAfterTheDelivery(t *testing.T) {
	rows := []DriverRow{
		gateRow(GatePhaseIssue, "gw_b", ip(20), 20, false),
		gateRow(GatePhaseDelivered, "gw_b", ip(21), 21, false), // the driver's ack of "pending"
		plainRow(ip(21), 21), // read before the verdict was consumed
	}
	if got := GateWaits(rows); !got[0].Open {
		t.Fatalf("a row with no request after the delivery closed the wait: %+v", got[0])
	}
	rows = append(rows, plainRow(ip(22), 22)) // consumed the verdict
	got := GateWaits(rows)
	if got[0].Open || got[0].ModelCalls == nil || *got[0].ModelCalls != 2 {
		t.Fatalf("closed live wait = %+v, want 2 (ack + consume; the issuing request is in the issue row)", got[0])
	}
}

// A harness that reports no count says so; it never reads as zero.
func TestGateWaits_UnavailableCountIsNamedNotZero(t *testing.T) {
	issue := gateRow(GatePhaseIssue, "gw_c", nil, 3, true)
	issue.Executable = "codex"
	issue.ModelCallsUnavailableReason = "codex: session rollout carries no model-call count"
	rows := []DriverRow{issue, gateRow(GatePhaseDelivered, "gw_c", nil, 3, true)}
	got := GateWaits(rows)
	if len(got) != 1 || got[0].ModelCalls != nil || got[0].Open {
		t.Fatalf("got %+v, want a settled-as-unavailable wait with no number", got)
	}
	if got[0].Reason != issue.ModelCallsUnavailableReason || FormatModelCalls(got[0]) != "—" {
		t.Fatalf("reason=%q rendered=%q, want the adapter-named reason and a dash", got[0].Reason, FormatModelCalls(got[0]))
	}
}

// A handle answered inline (an issue row, no delivery row) is not a wait the
// driver paid for; a delivery with no issue row is reported, not guessed at.
func TestGateWaits_OnlyDeliveredHandlesAreWaits(t *testing.T) {
	if got := GateWaits([]DriverRow{gateRow(GatePhaseIssue, "gw_d", ip(1), 1, true)}); len(got) != 0 {
		t.Fatalf("an undelivered handle is a wait: %+v", got)
	}
	got := GateWaits([]DriverRow{gateRow(GatePhaseDelivered, "gw_e", ip(1), 1, true)})
	if len(got) != 1 || got[0].Reason == "" || got[0].ModelCalls != nil {
		t.Fatalf("a delivery with no issue row = %+v, want a reason and no count", got)
	}
}

// Two handles, two sessions: rows of another session never close a wait.
func TestGateWaits_OtherSessionsRowsDoNotClose(t *testing.T) {
	rows := []DriverRow{
		gateRow(GatePhaseIssue, "gw_f", ip(10), 1, true),
		gateRow(GatePhaseDelivered, "gw_f", ip(10), 1, true),
	}
	other := plainRow(ip(99), 9)
	other.SessionID = "sess-2"
	rows = append(rows, other)
	if got := GateWaits(rows); !got[0].Open {
		t.Fatalf("another session's row closed this wait: %+v", got[0])
	}
}

func TestGateWaitClosedBy(t *testing.T) {
	w := GateWait{issueUnflushed: true, issueTurns: 4, deliveredCalls: 30}
	if w.ClosedBy(4, 99) || !w.ClosedBy(5, 0) {
		t.Fatal("a flush-lag wait closes on a later turn, whatever the calls")
	}
	w = GateWait{issueUnflushed: false, deliveredCalls: 30}
	if w.ClosedBy(99, 30) || !w.ClosedBy(0, 31) {
		t.Fatal("a live wait closes on a request after the delivery, whatever the turns")
	}
}

// The DRIVER table's CALLS column: the row's own count, "—" for an uncounted row,
// and a TOTAL that says how many rows it actually counted.
func TestFormatDriverRows_CallsColumn(t *testing.T) {
	counted := DriverRow{SessionID: "s", Available: true, ModelCalls: ip(3)}
	uncounted := DriverRow{SessionID: "s", Available: true}
	unavailable := DriverRow{SessionID: "s", Available: false, ModelCalls: ip(7)}
	views, total := FormatDriverRows([]DriverRow{counted, uncounted, unavailable})
	if views[0].Calls != "3" || views[1].Calls != "—" || views[2].Calls != "—" {
		t.Fatalf("calls column = %q %q %q, want 3 — —", views[0].Calls, views[1].Calls, views[2].Calls)
	}
	if total.Calls != "3 (1 of 2 rows counted)" {
		t.Fatalf("total calls = %q", total.Calls)
	}
	_, none := FormatDriverRows([]DriverRow{uncounted})
	if none.Calls != "—" {
		t.Fatalf("no counted row must total to a dash, got %q", none.Calls)
	}
}
