package verb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestRefuseHeldElsewhere(t *testing.T) {
	ClearHoldChecker()
	it := workitem.Item{ID: "sty_h", Status: workitem.StatusBacklog, Kind: workitem.KindStory}
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("nil checker: %v", err)
	}

	SetHoldChecker(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Holder: "loc_other", HolderLabel: "desk", LastSeen: "2026-09-01T00:00:00Z", HeldElsewhere: true}, nil
	})
	t.Cleanup(ClearHoldChecker)
	err := refuseHeldElsewhere(context.Background(), it)
	if err == nil {
		t.Fatal("expected refusal")
	}
	msg := err.Error()
	for _, want := range []string{"loc_other", "last seen", "hold takeover", "sty_h"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}

	SetHoldChecker(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Holder: "loc_self", HeldElsewhere: false}, nil
	})
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("held here: %v", err)
	}

	SetHoldChecker(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Unheld: true}, nil
	})
	err = refuseHeldElsewhere(context.Background(), it)
	if err == nil || !strings.Contains(err.Error(), "hold checkout") {
		t.Fatalf("unheld engage = %v", err)
	}
}

func TestRefuseHeldElsewhereFailOpen(t *testing.T) {
	SetHoldChecker(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{}, errors.New("network down")
	})
	t.Cleanup(ClearHoldChecker)
	it := workitem.Item{ID: "sty_h", Kind: workitem.KindStory}
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("lookup error must fail-open: %v", err)
	}
}
