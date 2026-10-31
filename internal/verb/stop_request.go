package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

func init() {
	Register(&Verb{
		Name:        "story-stop-request",
		Description: "Request stop of an engaged story (arbitrated at next step edge)",
		Invoke:      storyStopRequest,
	})
}

// storyStopRequest records a stop request on the story's ledger (AC5). It
// changes no status; the driver arbitrates at the next step edge
// (stopRequestBlocksForward).
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
	led, err := requireLedger()
	if err != nil {
		return nil, err
	}
	it, err := storyState(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if engaging, ok := storyStatusIsEngaging(ctx, it, it.Status); !ok || !engaging {
		return nil, fmt.Errorf("verb: %s is not performing (status %q) — nothing to stop", it.ID, it.Status)
	}
	requester := config.ResolveOwner()
	payload, _ := json.Marshal(ledger.StopRequestedPayload{By: requester, Reason: req.Reason})
	if _, err := led.Append(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindStopRequested, Actor: requester,
		Body: fmt.Sprintf("stop requested by %s: %s", requester, req.Reason), Payload: payload,
	}, time.Now()); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id":        req.ID,
		"requested": true,
		"by":        requester,
		"reason":    req.Reason,
	})
}
