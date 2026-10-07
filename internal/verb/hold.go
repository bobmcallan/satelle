package verb

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// HoldInfo is the outcome of claiming a story's hosted hold. Wired from the CLI
// (internal/hosted) so verb never imports hosted — same seam as assigneeResolver.
type HoldInfo struct {
	Holder        string
	HolderLabel   string
	LastSeen      string
	HeldElsewhere bool
}

// ErrHoldPending is returned by a claimer that could not place the hold — the
// server was unreachable or has never seen the story. The engage proceeds with a
// local hold, and the claimer has recorded the claim to be placed at the next
// work-state push (sty_52eb8c2f).
var ErrHoldPending = errors.New("hosted hold pending")

// holdClaimer places (or refreshes) the hosted hold for itemID and reports who
// holds it. Nil (unwired) means unbound / fully-local: refuseHeldElsewhere is a
// no-op (AC8).
var holdClaimer func(ctx context.Context, itemID string) (HoldInfo, error)

// SetHoldClaimer wires the hosted hold claim. Pass nil to clear (tests / unbound).
func SetHoldClaimer(f func(ctx context.Context, itemID string) (HoldInfo, error)) {
	holdClaimer = f
}

// ClearHoldClaimer unsets the claimer (tests). Unwired is the unbound path.
func ClearHoldClaimer() {
	holdClaimer = nil
}

// refuseHeldElsewhere claims the story's hosted hold for an engaging move, and
// refuses the move when another location holds it. It runs before the
// engagement lease, so a refusal takes none.
//
// An unreachable server or a story the server has never seen lets the move
// through with a warning (ErrHoldPending): a hosted outage must not brick local
// engagement (sty_dec88606). The claimer has recorded the claim, and the next
// push places the hold or reports the collision. Any other claimer error is
// treated the same way, unrecorded.
func refuseHeldElsewhere(ctx context.Context, current workitem.Item) error {
	if holdClaimer == nil {
		return nil
	}
	info, err := holdClaimer(ctx, current.ID)
	if err != nil {
		if errors.Is(err, ErrHoldPending) {
			fmt.Fprintf(os.Stderr, "satelle: hosted hold for %s not placed (engaging with a local hold; claimed at the next work-state push): %v\n", current.ID, err)
		} else {
			fmt.Fprintf(os.Stderr, "satelle: hosted hold lookup failed (engaging anyway): %v\n", err)
		}
		return nil
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
