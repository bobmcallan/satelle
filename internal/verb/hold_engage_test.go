package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestHeldElsewhereRefusesEngageNotListGetCancel(t *testing.T) {
	wire(t)
	verb.SetHoldChecker(func(ctx context.Context, itemID string) (verb.HoldInfo, error) {
		return verb.HoldInfo{Holder: "loc_other", LastSeen: "2026-09-01T00:00:00Z", HeldElsewhere: true}, nil
	})
	t.Cleanup(verb.ClearHoldChecker)

	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "held elsewhere"}), &it); err != nil {
		t.Fatal(err)
	}
	_, err := verb.Dispatch(context.Background(), "story-set", marshalReq(t, map[string]any{"id": it.ID, "status": "in_progress"}))
	if err == nil {
		t.Fatal("engage should refuse")
	}
	if !strings.Contains(err.Error(), "loc_other") || !strings.Contains(err.Error(), "hold takeover") {
		t.Fatalf("engage err = %v", err)
	}

	if _, err := verb.Dispatch(context.Background(), "story-get", marshalReq(t, map[string]any{"id": it.ID})); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := verb.Dispatch(context.Background(), "story-list", nil); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := verb.Dispatch(context.Background(), "story-set", marshalReq(t, map[string]any{"id": it.ID, "status": "cancelled"})); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestUnheldHostedStoryRefusesEngage(t *testing.T) {
	wire(t)
	verb.SetHoldChecker(func(ctx context.Context, itemID string) (verb.HoldInfo, error) {
		return verb.HoldInfo{Unheld: true}, nil
	})
	t.Cleanup(verb.ClearHoldChecker)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "unheld hosted"}), &it); err != nil {
		t.Fatal(err)
	}
	_, err := verb.Dispatch(context.Background(), "story-set", marshalReq(t, map[string]any{"id": it.ID, "status": "in_progress"}))
	if err == nil || !strings.Contains(err.Error(), "hold checkout") {
		t.Fatalf("unheld engage = %v", err)
	}
	if _, err := verb.Dispatch(context.Background(), "story-get", marshalReq(t, map[string]any{"id": it.ID})); err != nil {
		t.Fatalf("get: %v", err)
	}
}

func marshalReq(t *testing.T, req any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
