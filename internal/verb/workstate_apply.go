package verb

import (
	"context"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkstateApplier is invoked after a successful local work-item or ledger
// mutate. The CLI wires it to hosted Apply; nil (tests / local-only) is a
// no-op. verb must not import hosted — same reason as the assignee seam.
type WorkstateApplier func(ctx context.Context, items []workitem.Item, entries []ledger.Entry)

var workstateApplier WorkstateApplier

// SetWorkstateApplier wires the post-mutate Apply hook. Pass nil to clear.
func SetWorkstateApplier(f WorkstateApplier) {
	workstateApplier = f
}

// ClearWorkstateApplier unsets the hook (tests). Unwired means no Apply.
func ClearWorkstateApplier() {
	workstateApplier = nil
}

func fireWorkstateApply(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
	if workstateApplier == nil {
		return
	}
	workstateApplier(ctx, items, entries)
}
