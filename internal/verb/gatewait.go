package verb

import (
	"context"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Gate-wait rows (sty_c4b92c9e, epic:token-accountability).
//
// A handed-off gate run saves the driver the model calls of polling it. Whether
// it did is read from driver_usage rows, not asserted: a row as the command is
// issued, a row when the harness hook delivers the verdict, and — because the
// hook fires before the driver has consumed the verdict, and grok records a
// turn's usage only when the turn ends — a settling row once a read can see the
// turn the wait happened in. costview.GateWaits folds them into the count; this
// file only writes them. They are ordinary driver_usage rows (trigger "gate"),
// so their token and cost deltas add to the story's driver total like any other
// row's, and nothing is counted twice.

// DriverTriggerGate is the trigger of a gate-wait row.
const DriverTriggerGate = costview.GateTrigger

// RecordGateWait appends one gate-wait row for storyID: phase is one of
// costview.GatePhaseIssue and costview.GatePhaseDelivered, handle the gate
// run's. Best-effort like every driver_usage write — no session identity or no
// store records nothing and never fails the call it rides on.
func RecordGateWait(ctx context.Context, storyID, phase, handle string, now time.Time) {
	sessionID := config.ResolveSession()
	if sessionID == "" {
		return
	}
	recordDriverUsageAs(ctx, workitem.Item{ID: storyID}, sessionID, driverSessionHarness(sessionID),
		DriverTriggerGate, phase, handle, now)
}

// SettleGateWaits closes any delivered wait on storyID that has no row yet
// reading the turn it happened in. It reads the waiting session's own record
// now and, only if that record has by now flushed the turn, appends the settling
// row that lets the count be read. A wait whose turn is still unflushed is left
// open — the next call tries again — so a story cost read never writes a row
// that would close nothing.
func SettleGateWaits(ctx context.Context, storyID string, now time.Time) {
	if ledgerStore == nil {
		return
	}
	var rows []costview.DriverRow
	_ = ledgerStore.ForEachKind(ctx, storyID, ledger.KindDriverUsage, func(e ledger.Entry) error {
		if d, ok := costview.DecodeDriverRow(e); ok {
			rows = append(rows, d)
		}
		return nil
	})
	for _, w := range costview.GateWaits(rows) {
		if !w.Open {
			continue
		}
		snap := driverSnapshotter(w.Executable, w.SessionID, changeRecordRepoRoot())
		if !snap.Available || snap.ModelCallsUnavailableReason != "" || !w.ClosedBy(snap.Turns, snap.ModelCalls) {
			continue
		}
		recordDriverUsageAs(ctx, workitem.Item{ID: storyID}, w.SessionID, w.Executable,
			DriverTriggerGate, costview.GatePhaseSettled, w.Handle, now)
	}
}
