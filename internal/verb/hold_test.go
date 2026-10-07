package verb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestRefuseHeldElsewhere(t *testing.T) {
	ClearHoldClaimer()
	it := workitem.Item{ID: "sty_h", Status: workitem.StatusBacklog, Kind: workitem.KindStory}
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("nil claimer: %v", err)
	}

	SetHoldClaimer(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Holder: "loc_other", HolderLabel: "desk", LastSeen: "2026-09-01T00:00:00Z", HeldElsewhere: true}, nil
	})
	t.Cleanup(ClearHoldClaimer)
	err := refuseHeldElsewhere(context.Background(), it)
	if err == nil {
		t.Fatal("expected refusal")
	}
	msg := err.Error()
	for _, want := range []string{"loc_other", "desk", "last seen", "hold takeover", "sty_h"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}

	SetHoldClaimer(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Holder: "loc_self", HeldElsewhere: false}, nil
	})
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("held here: %v", err)
	}
}

func TestRefuseHeldElsewhereFailOpen(t *testing.T) {
	it := workitem.Item{ID: "sty_h", Kind: workitem.KindStory}
	t.Cleanup(ClearHoldClaimer)
	for name, claimErr := range map[string]error{
		"lookup error": errors.New("network down"),
		"pending":      fmt.Errorf("%w: server down", ErrHoldPending),
	} {
		SetHoldClaimer(func(ctx context.Context, itemID string) (HoldInfo, error) {
			return HoldInfo{}, claimErr
		})
		if err := refuseHeldElsewhere(context.Background(), it); err != nil {
			t.Fatalf("%s must fail-open: %v", name, err)
		}
	}
}
