package verb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestRefuseHeldElsewhere(t *testing.T) {
	withWiring(t)
	ClearHoldClaimer()
	it := workitem.Item{ID: "sty_h", Status: workitem.StatusBacklog, Kind: workitem.KindStory}
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("nil claimer: %v", err)
	}

	SetHoldClaimer(func(ctx context.Context, itemID string) (HoldInfo, error) {
		return HoldInfo{Holder: "loc_other", HolderLabel: "desk", LastSeen: "2026-09-01T00:00:00Z", HeldElsewhere: true}, nil
	})
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

// AC4 (sty_6e5fc41c): the refusal stays, and it names the holder, its last-seen
// and whether the holder is live or presumed dead.
func TestRefuseHeldElsewhereSaysLiveOrPresumedDead(t *testing.T) {
	withWiring(t)
	it := workitem.Item{ID: "sty_h", Status: workitem.StatusBacklog, Kind: workitem.KindStory}
	for _, tc := range []struct {
		name string
		info HoldInfo
		want string
		not  string
	}{
		{"fresh", HoldInfo{Holder: "loc_other", HolderLabel: "desk", LastSeen: "2026-10-14T11:55:00Z", HeldElsewhere: true, Age: 5 * time.Minute}, "holder live", "presumed dead"},
		{"stale", HoldInfo{Holder: "loc_other", HolderLabel: "desk", LastSeen: "2026-10-14T05:00:00Z", HeldElsewhere: true, PresumedDead: true, Age: 7 * time.Hour}, "holder presumed dead (no sign for 7h0m0s)", "holder live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetHoldClaimer(func(context.Context, string) (HoldInfo, error) { return tc.info, nil })
			err := refuseHeldElsewhere(context.Background(), it)
			if err == nil {
				t.Fatal("a held story must stay refused")
			}
			msg := err.Error()
			for _, want := range []string{"loc_other", "desk", "last seen " + tc.info.LastSeen, tc.want, "hold takeover sty_h"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal missing %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, tc.not) {
				t.Errorf("refusal must not say %q: %s", tc.not, msg)
			}
		})
	}
}

// An execution is append-only and never held: engaging one claims no hold, even
// when the claimer would report another location as its holder.
func TestRefuseHeldElsewhereClaimsNoHoldForAnExecution(t *testing.T) {
	withWiring(t)
	called := false
	SetHoldClaimer(func(ctx context.Context, itemID string) (HoldInfo, error) {
		called = true
		return HoldInfo{Holder: "loc_other", HeldElsewhere: true}, nil
	})
	it := workitem.Item{ID: "exe_h", Status: workitem.StatusBacklog, Kind: workitem.KindExecution}
	if err := refuseHeldElsewhere(context.Background(), it); err != nil {
		t.Fatalf("an execution must not be refused: %v", err)
	}
	if called {
		t.Fatal("the hold claimer was called for an execution")
	}
}

func TestRefuseHeldElsewhereFailOpen(t *testing.T) {
	it := workitem.Item{ID: "sty_h", Kind: workitem.KindStory}
	withWiring(t)
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
