package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// StatusDrift is one work item whose row.status disagrees with the last
// status_transition on its ledger. The ledger is the source of truth.
type StatusDrift struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	RowStatus    string    `json:"row_status"`
	LedgerStatus string    `json:"ledger_status"`
	RowUpdatedAt time.Time `json:"row_updated_at"`
	TransitionAt time.Time `json:"transition_at"`
	TransitionID string    `json:"transition_id"`
}

// TransitionTo reads the destination status from a status_transition payload.
// Falls back to the "from → to" body only when the payload has no `to`.
func TransitionTo(e ledger.Entry) string {
	var p struct {
		To string `json:"to"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) == nil {
		if to := strings.TrimSpace(p.To); to != "" {
			return to
		}
	}
	if i := strings.LastIndex(e.Body, " → "); i >= 0 {
		return strings.TrimSpace(e.Body[i+len(" → "):])
	}
	return ""
}

// TransitionFrom reads the source status from a status_transition payload.
// Falls back to the "from → to" body only when the payload has no `from`.
func TransitionFrom(e ledger.Entry) string {
	var p struct {
		From string `json:"from"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) == nil {
		if from := strings.TrimSpace(p.From); from != "" {
			return from
		}
	}
	if i := strings.Index(e.Body, " → "); i >= 0 {
		return strings.TrimSpace(e.Body[:i])
	}
	return ""
}

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

// DetectStatusDrift reports non-archived work items whose row.status disagrees
// with the last status_transition. Items with no transition events are not drift.
func DetectStatusDrift(ctx context.Context, stories *workitem.Store, led *ledger.Store) ([]StatusDrift, error) {
	if stories == nil || led == nil {
		return nil, fmt.Errorf("verb: detect status drift needs stories and ledger stores")
	}
	items, err := stories.List(ctx, workitem.ListFilter{Limit: 2000})
	if err != nil {
		return nil, err
	}
	var out []StatusDrift
	for _, it := range items {
		e, ok, lerr := LatestStatusTransition(ctx, led, it.ID)
		if lerr != nil {
			return nil, lerr
		}
		if !ok {
			continue
		}
		to := TransitionTo(e)
		if to == "" || to == it.Status {
			continue
		}
		out = append(out, StatusDrift{
			ID:           it.ID,
			Title:        it.Title,
			Kind:         string(it.Kind),
			RowStatus:    it.Status,
			LedgerStatus: to,
			RowUpdatedAt: it.UpdatedAt,
			TransitionAt: e.CreatedAt,
			TransitionID: e.ID,
		})
	}
	return out, nil
}

// RepairStatusDrift sets each drifted row's status from its last
// status_transition and appends a status_reconcile evidence row.
func RepairStatusDrift(ctx context.Context, stories *workitem.Store, led *ledger.Store, now time.Time) (repaired []StatusDrift, err error) {
	drifts, err := DetectStatusDrift(ctx, stories, led)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, d := range drifts {
		if _, uerr := stories.SetStatus(ctx, d.ID, d.LedgerStatus, now); uerr != nil {
			return repaired, fmt.Errorf("repair %s: %w", d.ID, uerr)
		}
		payload, _ := json.Marshal(map[string]string{
			"from":          d.RowStatus,
			"to":            d.LedgerStatus,
			"transition_id": d.TransitionID,
		})
		if _, aerr := led.Append(ctx, ledger.AppendInput{
			StoryID: d.ID,
			Kind:    ledger.KindStatusReconcile,
			Actor:   "reconcile",
			Body:    fmt.Sprintf("reconciled status %s → %s from %s", d.RowStatus, d.LedgerStatus, d.TransitionID),
			Payload: payload,
		}, now); aerr != nil {
			return repaired, fmt.Errorf("repair %s: record: %w", d.ID, aerr)
		}
		repaired = append(repaired, d)
	}
	return repaired, nil
}
