package verb

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// PriorVerdict is one verdict already recorded for an item on one from→to edge.
// Decision is "accept" or "reject"; CreatedAt is RFC3339.
type PriorVerdict struct {
	Skill     string `json:"skill,omitempty"`
	Decision  string `json:"decision"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// priorVerdictRow is the reviewer row's payload as reviewerPayload
// (workitem.go) writes it — the decision itself comes from the row's KIND,
// which is what the trail is indexed by.
type priorVerdictRow struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Skill string `json:"skill,omitempty"`
	Notes string `json:"notes,omitempty"`
}

// PriorVerdicts returns every review verdict already recorded for itemID on the
// from→to edge, oldest first — the enumeration a re-reviewing gate needs to
// judge the delta rather than re-deriving a verdict from scratch
// (sty_0f5e600c). Rows for other edges of the same story are excluded. The
// ledger already carries the edge in each reviewer row's payload, so this is a
// read: no schema change, and history written before the feature works
// retroactively.
//
// Nil-safe when no ledger is wired, and never an error the caller must handle
// specially: prior verdicts are additive context, so an unreadable row is
// skipped rather than failing the transition it decorates.
func PriorVerdicts(ctx context.Context, itemID, from, to string) ([]PriorVerdict, error) {
	if ledgerStore == nil || strings.TrimSpace(itemID) == "" {
		return nil, nil
	}
	entries, err := ledgerStore.ListByStory(ctx, itemID, "")
	if err != nil {
		return nil, err
	}
	var out []PriorVerdict
	for _, e := range entries {
		var decision string
		switch e.Kind {
		case ledger.KindReviewAccept:
			decision = "accept"
		case ledger.KindReviewReject:
			decision = "reject"
		default:
			continue
		}
		var row priorVerdictRow
		if err := json.Unmarshal(e.Payload, &row); err != nil {
			continue
		}
		if row.From != from || row.To != to {
			continue
		}
		out = append(out, PriorVerdict{
			Skill:     row.Skill,
			Decision:  decision,
			Notes:     row.Notes,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}
