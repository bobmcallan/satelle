package verb

import (
	"context"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// HoldInfo is the hosted checkout of a story, if any. Wired from the CLI
// (internal/hosted) so verb never imports hosted — same seam as assigneeResolver.
type HoldInfo struct {
	Holder        string
	HolderLabel   string
	LastSeen      string
	HeldElsewhere bool
	// Unheld is a hosted story with no location. Engage is refused until
	// satelle story hold checkout (AC2). Distinct from HeldElsewhere (AC4)
	// and from a lookup error / missing item (fail-open: local-only).
	Unheld bool
}

// holdChecker returns the current hosted hold for itemID. Nil (unwired) means
// unbound / fully-local: refuseHeldElsewhere is a no-op (AC6).
var holdChecker func(ctx context.Context, itemID string) (HoldInfo, error)

// SetHoldChecker wires hosted hold lookup. Pass nil to clear (tests / unbound).
func SetHoldChecker(f func(ctx context.Context, itemID string) (HoldInfo, error)) {
	holdChecker = f
}

// ClearHoldChecker unsets the checker (tests). Unwired is the unbound path.
func ClearHoldChecker() {
	holdChecker = nil
}

// refuseHeldElsewhere refuses an engaging move when another location holds the
// story. Nil checker, lookup error, or !HeldElsewhere allow the move.
//
// Fail-open on lookup error is deliberate (sty_dec88606): a hosted outage must
// not brick local engagement. The warning goes to stderr so the operator sees
// the degraded check; two locations can then engage the same story until
// connectivity returns.
func refuseHeldElsewhere(ctx context.Context, current workitem.Item) error {
	if holdChecker == nil {
		return nil
	}
	info, err := holdChecker(ctx, current.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "satelle: hosted hold lookup failed (engaging anyway): %v\n", err)
		return nil
	}
	if info.Unheld {
		return fmt.Errorf("%s is unheld on the hosted server — satelle story hold checkout %s", current.ID, current.ID)
	}
	if !info.HeldElsewhere {
		return nil
	}
	seen := info.LastSeen
	if seen != "" {
		seen = ", last seen " + seen
	}
	label := info.HolderLabel
	if label != "" {
		label = " (" + label + ")"
	}
	return fmt.Errorf("%s is held by location %s%s%s — satelle story hold takeover %s",
		current.ID, info.Holder, label, seen, current.ID)
}
