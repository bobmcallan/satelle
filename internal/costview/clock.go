// Package costview is the single owner of every cost figure satelle computes
// from the ledger: an item's own dollars/tokens/time, its family roll-up, and
// the per-skill/per-seat gate-value view. It performs no I/O and reads no
// store — every function takes its inputs (ledger entries, items, a Clock) as
// arguments, so the CLI and web loaders that call it always print the same
// numbers (sty_b8542a3a).
//
// costview holds no status literals. The story clock's shape (which
// transition starts it, which ends it) is supplied by the caller as a Clock —
// see wfgovern.ClockFor, which is the one place that resolves a workflow spec
// into these predicates.
package costview

// Clock is the pair of shape-derived predicates that mark the story clock's
// start and end. Engaging(to) is true for a non-terminal engaging state;
// Terminal(to) is true only for a terminal state, never a park — a
// blocked/park transition leaves the story open and must not stop the clock.
type Clock struct {
	Engaging func(to string) bool
	Terminal func(to string) bool
}
