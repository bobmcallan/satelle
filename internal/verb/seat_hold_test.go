package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestSeatHeldThroughLongGate (sty_7f3e6fd3 AC1): a gate that runs past
// HeartbeatTTL fires no hook, so only the keep-alive the verb runs around it
// stands between the seat and Reap. The stub ages the heartbeat as a long wait
// would, then waits for the keep-alive to refresh it; the pid probe says dead so
// nothing but the heartbeat can vouch for the holder.
func TestSeatHeldThroughLongGate(t *testing.T) {
	withWiring(t)
	prevProbe := lease.PidAlive
	lease.PidAlive = func(int) bool { return false }
	prevEvery := lease.KeepAliveInterval
	lease.KeepAliveInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		lease.PidAlive = prevProbe
		lease.KeepAliveInterval = prevEvery
	})
	db := wireWithWorkflowsStore(t, singleStoryWF)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)

	var aliveDuringGate bool
	verb.SetTransitionGater(gaterFunc(func(item workitem.Item, _ string) verb.GateDecision {
		ctx := context.Background()
		if err := db.Leases.SetHeartbeat(ctx, item.ID, time.Now().UTC().Add(-lease.HeartbeatTTL-time.Minute)); err != nil {
			t.Errorf("age heartbeat: %v", err)
		}
		deadline := time.Now().Add(testutil.WaitBudget)
		for time.Now().Before(deadline) {
			if l, err := db.Leases.Get(ctx, item.ID); err == nil && lease.Alive(l, time.Now().UTC()) {
				aliveDuringGate = true
				break
			}
			time.Sleep(5 * time.Millisecond) // poll tick
		}
		if reaped, _ := db.Leases.Reap(ctx); len(reaped) != 0 {
			t.Errorf("Reap took the seat of a story whose gate is running: %+v", reaped)
		}
		return verb.GateDecision{Gated: false}
	}))

	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if !aliveDuringGate {
		t.Fatal("the seat was not held alive while the gate ran")
	}
	if _, err := db.Leases.Get(context.Background(), a.ID); err != nil {
		t.Fatalf("seat missing after the transition: %v", err)
	}
}

// TestGateRejectKeepsPreexistingSeat: a rejected edge on a story that already
// holds its seat clears only the in-flight mark — the seat row is not the
// gate's to delete, only one this call created.
func TestGateRejectKeepsPreexistingSeat(t *testing.T) {
	withWiring(t)
	db := wireWithWorkflowsStore(t, singleStoryWF)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	before, err := db.Leases.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("seat after engage: %v", err)
	}

	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: false, Skill: "s", Notes: "no"}})
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}); err == nil {
		t.Fatal("expected the gate to reject")
	}

	after, err := db.Leases.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("a gate reject dropped a pre-existing seat: %v", err)
	}
	if !after.AcquiredAt.Equal(before.AcquiredAt) {
		t.Fatalf("seat was re-created on reject: acquired %v -> %v", before.AcquiredAt, after.AcquiredAt)
	}
	if after.InFlight {
		t.Fatal("in_flight must be cleared after a reject so a retry can re-enter")
	}
}
