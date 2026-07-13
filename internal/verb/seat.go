package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
)

func init() {
	Register(&Verb{
		Name:        "story-seat-list",
		Description: "List engagement seat leases (reaps stale rows first)",
		Invoke:      storySeatList,
	})
	Register(&Verb{
		Name:        "story-seat-release",
		Description: "Force-release an engagement seat by story/task id (operator override)",
		Invoke:      storySeatRelease,
	})
}

// seatRow is one engagement_lease row for the agent/operator surface.
type seatRow struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	StorySeat     bool   `json:"story_seat"`
	Owner         string `json:"owner"`
	State         string `json:"state"`
	InFlight      bool   `json:"in_flight"`
	AcquiredAge   string `json:"acquired_age"`
	HeartbeatAge  string `json:"heartbeat_age"`
	Stale         bool   `json:"stale"`
	StopRequested string `json:"stop_requested_by,omitempty"`
	StopReason    string `json:"stop_reason,omitempty"`
}

// storySeatList reaps stale leases then lists remaining rows with age/stale flags
// (sty_1738f973 AC4). Reap is opportunistic honesty; gate correctness does not
// depend on it.
func storySeatList(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	ls, err := requireLease()
	if err != nil {
		return nil, err
	}
	if _, err := ls.Reap(ctx); err != nil {
		return nil, err
	}
	all, err := ls.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]seatRow, 0, len(all))
	for _, l := range all {
		out = append(out, seatRowFromLease(l, now))
	}
	return json.Marshal(out)
}

// storySeatRelease force-releases a seat regardless of owner (operator/agent
// recovery for an orphaned or stuck holder — sty_1738f973 AC4).
func storySeatRelease(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	ls, err := requireLease()
	if err != nil {
		return nil, err
	}
	if err := ls.ForceRelease(ctx, req.ID); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id":       req.ID,
		"released": true,
	})
}

func seatRowFromLease(l lease.Lease, now time.Time) seatRow {
	return seatRow{
		ID:            l.ItemID,
		Kind:          l.Kind,
		StorySeat:     l.StorySeat,
		Owner:         l.Owner,
		State:         l.State,
		InFlight:      l.InFlight,
		AcquiredAge:   formatAge(now.Sub(l.AcquiredAt)),
		HeartbeatAge:  formatAge(now.Sub(l.HeartbeatAt)),
		Stale:         lease.IsStale(l, now),
		StopRequested: l.StopRequestedBy,
		StopReason:    l.StopReason,
	}
}

// formatAge renders a duration as a short human age (minutes/hours/days).
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	return fmt.Sprintf("%dd%dh", days, int(d.Hours())%24)
}
