package costview

import "fmt"

// Gate waits (sty_c4b92c9e, epic:token-accountability).
//
// A gate-running verb hands the run to a detached process and the driver ends
// its turn; the harness delivers the verdict as a notification. What that saves
// is the driver's own model calls — so the wait is judged by how many the
// driving session made between issuing the command and consuming the verdict,
// read from driver_usage rows rather than asserted.
//
// A wait is bounded by two rows and a third that closes it:
//
//	issue      taken as the gate command starts (trigger "gate", from "issue")
//	delivered  taken when the harness hook hands the verdict to the session
//	closing    the first row of the session after the delivery whose cumulative
//	           includes the turn the wait happened in
//
// The delivered row cannot close the window by itself: it is read by the hook
// before the driver has consumed the verdict, and a harness that writes a
// turn's usage only when the turn ends (Unflushed) has not yet recorded the
// turn at all. So the count is a difference of absolute cumulative model-call
// counts — closing minus issue — and it is only readable once a row taken after
// the flush exists. Until then the wait is reported open, never as zero.
//
// The count is an upper bound on the wait's own calls: everything the session
// requested between the two reads is counted, including work it did around the
// wait. It can overstate a wait; it cannot hide a driver that polled.

// GateTrigger is the driver_usage trigger of a gate-wait row.
const GateTrigger = "gate"

// Gate-wait phases, carried in a gate row's From; the handle is in To.
const (
	GatePhaseIssue     = "issue"
	GatePhaseDelivered = "delivered"
	GatePhaseSettled   = "settled"
)

// GateWait is one handed-off gate run's cost to the driving session.
type GateWait struct {
	Handle     string `json:"handle"`
	SessionID  string `json:"session_id"`
	Executable string `json:"executable"`

	// ModelCalls is the driver's model requests across the wait; nil while the
	// window is open, or when the harness cannot count them (Reason says which).
	ModelCalls *int `json:"model_calls,omitempty"`
	// Open is true while no closing row exists yet.
	Open   bool   `json:"open,omitempty"`
	Reason string `json:"reason,omitempty"`

	// What the issue and delivery rows read — what a later read must exceed to
	// close the wait (see ClosedBy).
	issueTurns     int
	issueUnflushed bool
	deliveredCalls int
}

// ClosedBy reports whether a read of the session — its turn count and its model
// calls — closes this wait. For a harness that records a turn's usage only when
// the turn ends, that is a read from a later turn than the issue row saw: the
// turn the wait happened in is then in it. For a harness that records live, it
// is a read with a request after the delivery, since the delivery row is taken
// before the driver has consumed the verdict.
func (w GateWait) ClosedBy(turns, calls int) bool {
	if w.issueUnflushed {
		return turns > w.issueTurns
	}
	return calls > w.deliveredCalls
}

func isGateRow(d DriverRow, phase string) bool {
	return d.Trigger == GateTrigger && d.From == phase
}

// GateWaits folds a story's driver rows, in ledger order, into one GateWait per
// handle that was delivered. A handle with an issue row and no delivery row was
// answered inline or is still running — not a wait the driver paid for.
func GateWaits(rows []DriverRow) []GateWait {
	var out []GateWait
	for i, r := range rows {
		if !isGateRow(r, GatePhaseDelivered) {
			continue
		}
		w := GateWait{Handle: r.To, SessionID: r.SessionID, Executable: r.Executable}
		issue, ok := findGateRow(rows[:i], GatePhaseIssue, r.To, r.SessionID)
		w.issueTurns, w.issueUnflushed = issue.Turns, issue.Unflushed
		switch {
		case !ok:
			w.Reason = "no issue row was recorded for this handle"
		case !issue.Available || issue.Cumulative.ModelCalls == nil:
			w.Reason = unavailableModelCalls(issue)
		default:
			w.deliveredCalls = *issue.Cumulative.ModelCalls
			if r.Available && r.Cumulative.ModelCalls != nil {
				w.deliveredCalls = *r.Cumulative.ModelCalls
			}
			closing, found := closingRow(rows[i+1:], w)
			if !found {
				w.Open = true
				w.Reason = "no row after the delivery has read the turn the wait happened in"
				if issue.Unflushed {
					w.Reason += ": " + issue.Executable + " records a turn's usage only when the turn ends"
				}
				break
			}
			calls := *closing.Cumulative.ModelCalls - *issue.Cumulative.ModelCalls
			// A flush-lagged harness's issue row was read inside the turn that made
			// the call, so that turn's own issuing request is in the closing row and
			// not in the issue row: it is not part of the wait.
			if w.issueUnflushed {
				calls--
			}
			if calls < 0 {
				calls = 0
			}
			w.ModelCalls = &calls
		}
		out = append(out, w)
	}
	return out
}

func findGateRow(rows []DriverRow, phase, handle, session string) (DriverRow, bool) {
	for i := len(rows) - 1; i >= 0; i-- {
		if isGateRow(rows[i], phase) && rows[i].To == handle && rows[i].SessionID == session {
			return rows[i], true
		}
	}
	return DriverRow{}, false
}

// closingRow is the first row after the delivery that closes w: the wait's
// session, measured, carrying a model-call count, and satisfying ClosedBy.
func closingRow(rows []DriverRow, w GateWait) (DriverRow, bool) {
	for _, r := range rows {
		if r.SessionID != w.SessionID || !r.Available || r.Cumulative.ModelCalls == nil {
			continue
		}
		if w.ClosedBy(r.Turns, *r.Cumulative.ModelCalls) {
			return r, true
		}
	}
	return DriverRow{}, false
}

func unavailableModelCalls(r DriverRow) string {
	if r.ModelCallsUnavailableReason != "" {
		return r.ModelCallsUnavailableReason
	}
	return fmt.Sprintf("%s: the issue row carries no model-call count", r.Executable)
}

// FormatModelCalls renders a wait's count: the number, "open" while unsettled,
// or "—" when the harness cannot count — never a zero standing in for a wait
// nothing has measured.
func FormatModelCalls(w GateWait) string {
	switch {
	case w.ModelCalls != nil:
		return fmt.Sprintf("%d", *w.ModelCalls)
	case w.Open:
		return "open"
	default:
		return "—"
	}
}
