package verb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satelle/internal/lease"
)

func init() {
	Register(&Verb{
		Name:        "story-stop-request",
		Description: "Request stop of an engaged story (arbitrated at next step edge)",
		Invoke:      storyStopRequest,
	})
}

// storyStopRequest annotates the holder's lease with a stop request (AC5).
// Never releases; the owner arbitrates at the next step edge.
func storyStopRequest(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
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
	requester := lease.ResolveOwner()
	if err := ls.RequestStop(ctx, req.ID, requester, req.Reason); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id":        req.ID,
		"requested": true,
		"by":        requester,
		"reason":    req.Reason,
	})
}
