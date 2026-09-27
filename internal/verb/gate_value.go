package verb

import (
	"context"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// GateValueOptions scopes ComputeGateValue: exactly one of StoryID or EpicID
// narrows the story set (StoryID wins if both are set); with neither, the
// scan covers every story in the repo. Since/Until (either may be zero) bound
// the date range on entry timestamps regardless of story scope.
type GateValueOptions struct {
	StoryID string
	EpicID  string
	Since   time.Time
	Until   time.Time
}

// ComputeGateValue reports invocations, dollars, fresh tokens, accepts,
// rejects and dollars-per-reject per skill/seat (sty_b8542a3a AC6) — a QUERY
// over stored evidence, recommending nothing and changing no configuration.
// Story/epic scoping picks WHICH stories' ledgers feed costview.GateValue
// (the same parent_id walk AC5's family view uses for --epic); costview
// itself applies only the date-range filter, since it does no I/O.
func ComputeGateValue(ctx context.Context, opts GateValueOptions) (costview.GateValueReport, error) {
	ls, err := requireLedger()
	if err != nil {
		return costview.GateValueReport{}, err
	}

	entriesByID := map[string][]ledger.Entry{}
	switch {
	case opts.StoryID != "":
		es, err := ls.ListByStory(ctx, opts.StoryID, "")
		if err != nil {
			return costview.GateValueReport{}, err
		}
		entriesByID[opts.StoryID] = es

	case opts.EpicID != "":
		wi, err := requireWorkItem()
		if err != nil {
			return costview.GateValueReport{}, err
		}
		root, err := wi.Get(ctx, opts.EpicID)
		if err != nil {
			return costview.GateValueReport{}, err
		}
		var descendants []workitem.Item
		if err := collectDescendants(ctx, wi, root.ID, map[string]bool{root.ID: true}, &descendants); err != nil {
			return costview.GateValueReport{}, err
		}
		ids := []string{root.ID}
		for _, d := range descendants {
			ids = append(ids, d.ID)
		}
		for _, id := range ids {
			es, err := ls.ListByStory(ctx, id, "")
			if err != nil {
				return costview.GateValueReport{}, err
			}
			entriesByID[id] = es
		}

	default:
		// Repo-wide: every story's agent_invocation and review rows, paged
		// internally by ForEachKind so a scan larger than one page is never
		// silently truncated. The bucket key is unused by costview.GateValue
		// (it only ranges over the map's values), so one key covers the scan.
		var all []ledger.Entry
		for _, kind := range []string{ledger.KindAgentInvocation, ledger.KindReviewAccept, ledger.KindReviewReject} {
			if err := ls.ForEachKind(ctx, "", kind, func(e ledger.Entry) error {
				all = append(all, e)
				return nil
			}); err != nil {
				return costview.GateValueReport{}, err
			}
		}
		entriesByID["*"] = all
	}

	return costview.GateValue(entriesByID, costview.GateFilter{Since: opts.Since, Until: opts.Until}), nil
}
