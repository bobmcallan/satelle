package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A story's cached row is checked against the fold of its ledger chain before any
// of its events are published (sty_d84be8bc): the row is what a push reads its
// item record from, and a row that disagrees with the chain would publish a story
// the chain does not describe. A story that disagrees is withheld the way a held
// story is — its rows stay behind the push cursors — and the report says which
// field disagrees and what the chain holds. Nothing is repaired here; `satelle
// story reconcile --repair` rebuilds the row, after which the next push sends the
// withheld events.

// driftResult is the outcome of validating a batch's stories.
type driftResult struct {
	held  map[string]string // story id -> reason
	lines []string          // one per drifted field: story, field, the chain's value
}

// classifyFoldDrift validates every story the batch names, as an item row or as
// the owner of a ledger row. Only stories are folded; a task or execution in the
// batch is never withheld.
func classifyFoldDrift(ctx context.Context, a *app.App, items []json.RawMessage, rows []ledgerRowMeta) (driftResult, error) {
	ids := map[string]bool{}
	for _, raw := range items {
		if id := rawItemID(raw); id != "" {
			ids[id] = true
		}
	}
	for _, r := range rows {
		if r.storyID != "" {
			ids[r.storyID] = true
		}
	}
	res := driftResult{held: map[string]string{}}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		row, err := a.Store.Stories.Get(ctx, id)
		if err != nil || row.Kind != workitem.KindStory {
			continue // not a story on this machine: nothing to fold
		}
		drifts, err := verb.StoryDrifts(ctx, a.Store.Ledger, row)
		if err != nil {
			return res, fmt.Errorf("validate %s against its ledger chain: %w", id, err)
		}
		if len(drifts) == 0 {
			continue
		}
		fields := make([]string, 0, len(drifts))
		for _, d := range drifts {
			fields = append(fields, d.Field)
			res.lines = append(res.lines, fmt.Sprintf("withheld %s: %s disagrees with its ledger chain: row=%q, ledger chain=%q",
				d.ID, d.Field, d.Row, d.Chain))
		}
		res.held[id] = fmt.Sprintf("cached row disagrees with its ledger chain on %s — run `satelle story reconcile --repair`", strings.Join(fields, ", "))
	}
	return res, nil
}
