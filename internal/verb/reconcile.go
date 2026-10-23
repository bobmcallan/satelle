package verb

import (
	"context"
	"encoding/json"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// LatestStatusTransition returns the newest status_transition for storyID.
func LatestStatusTransition(ctx context.Context, led *ledger.Store, storyID string) (ledger.Entry, bool, error) {
	if led == nil {
		return ledger.Entry{}, false, nil
	}
	entries, err := led.ListByStory(ctx, storyID, ledger.KindStatusTransition)
	if err != nil {
		return ledger.Entry{}, false, err
	}
	if len(entries) == 0 {
		return ledger.Entry{}, false, nil
	}
	return entries[len(entries)-1], true, nil
}

// reconcilePayload is a status_reconcile event's payload: each field the row
// held and the chain's value it was rebuilt to. The fold ignores the event, since
// it records a repair of the cache, not a change to the story.
func reconcilePayload(drifts []StoryDrift) json.RawMessage {
	type field struct {
		Field string `json:"field"`
		Row   string `json:"row"`
		Chain string `json:"chain"`
	}
	fields := make([]field, 0, len(drifts))
	for _, d := range drifts {
		fields = append(fields, field{Field: d.Field, Row: d.Row, Chain: d.Chain})
	}
	b, _ := json.Marshal(map[string]any{"fields": fields})
	return b
}
