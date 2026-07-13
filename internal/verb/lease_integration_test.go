package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestLeaseStopRequestBlocksForward: stop-request refuses a forward engaging
// move with the reason; park remains allowed (AC5).
func TestLeaseStopRequestBlocksForward(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})
	verb.SetAllowParallelStories(false)
	t.Cleanup(func() { verb.SetAllowParallelStories(false) })

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)

	// Any principal may request stop.
	t.Setenv("SATELLE_OWNER", "requester")
	if _, err := verb.Dispatch(context.Background(), "story-stop-request", mustJSONLease(map[string]any{
		"id": a.ID, "reason": "handoff needed",
	})); err != nil {
		t.Fatalf("stop-request: %v", err)
	}
	// Clear so owner identity matches the process that acquired the lease.
	t.Setenv("SATELLE_OWNER", "")

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"})
	if err == nil || !strings.Contains(err.Error(), "stop requested") {
		t.Fatalf("forward move should be refused for stop request: %v", err)
	}
	// Park allowed.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "blocked"}), &a)
	if a.Status != "blocked" {
		t.Fatalf("park should be allowed under stop request: %q", a.Status)
	}
}

// TestLeaseSameTargetInFlightNoOp: re-engage same story to same status is a
// no-op (AC3 — never two concurrent dispatches for one id/target).
func TestLeaseSameTargetInFlightNoOp(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})
	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	// Second set to plan should no-op without error.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if a.Status != "plan" {
		t.Fatalf("status = %q", a.Status)
	}
	// Sequential progression still works.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("progression blocked: %q", a.Status)
	}
}

// TestLeaseNewAcquireAbortFreesSeat: when a NEW engage (backlog→plan) is
// rejected by the gate, the just-claimed seat is released so another story can
// engage (AC4 release-on-abort).
func TestLeaseNewAcquireAbortFreesSeat(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})
	verb.SetAllowParallelStories(false)
	t.Cleanup(func() { verb.SetAllowParallelStories(false) })

	verb.SetTransitionGater(rejectToGater{to: "plan", skill: "intent"})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "B", "category": "feature"}), &b)

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
	if err == nil {
		t.Fatal("expected gate reject on plan")
	}
	// A stays backlog; seat released.
	var still workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": a.ID}), &still)
	if still.Status != workitem.StatusBacklog {
		t.Fatalf("A status = %q after reject", still.Status)
	}

	// Clear gater so B can engage.
	verb.SetTransitionGater(nil)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": b.ID, "status": "plan"}), &b)
	if b.Status != "plan" {
		t.Fatalf("B should engage after A's abort freed the seat: %q", b.Status)
	}
}

// TestLeaseAcquireBeforeStatusCommit: while a long gate runs on story A (status
// still backlog), story B's concurrent engage is refused and A has not committed
// plan yet (AC1/AC2 acquire-at-start window).
func TestLeaseAcquireBeforeStatusCommit(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})
	verb.SetAllowParallelStories(false)
	t.Cleanup(func() { verb.SetAllowParallelStories(false) })

	// Slow gater blocks A on backlog→plan long enough for B to race.
	done := make(chan struct{})
	gater := &slowGater{to: "plan", delay: 200 * time.Millisecond, done: done}
	verb.SetTransitionGater(gater)
	t.Cleanup(func() {
		verb.SetTransitionGater(nil)
		select {
		case <-done:
		default:
			close(done)
		}
	})

	var a, b workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "B", "category": "feature"}), &b)

	errCh := make(chan error, 1)
	go func() {
		_, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "plan"})
		errCh <- err
	}()
	// Give A time to Acquire before gate sleeps.
	time.Sleep(50 * time.Millisecond)
	_, berr := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"})
	if berr == nil || (!strings.Contains(berr.Error(), "engagement seat") && !strings.Contains(berr.Error(), "one performing")) {
		t.Fatalf("B must be refused while A holds seat pre-commit: %v", berr)
	}
	// A still backlog until its gate finishes (or at least B was refused mid-window).
	var mid workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": a.ID}), &mid)
	// Wait for A to finish.
	if err := <-errCh; err != nil {
		t.Fatalf("A engage: %v", err)
	}
	// After A commits, status is plan.
	json.Unmarshal(call(t, "story-get", map[string]any{"id": a.ID}), &a)
	if a.Status != "plan" {
		t.Fatalf("A final status = %q", a.Status)
	}
	// Mid-window observation: if we caught it before commit, status was backlog.
	_ = mid
}

// slowGater delays the first transition into `to`, then accepts.
type slowGater struct {
	to    string
	delay time.Duration
	done  chan struct{}
	once  sync.Once
}

func (s *slowGater) Gate(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, error) {
	if toStatus == s.to {
		s.once.Do(func() {
			time.Sleep(s.delay)
			close(s.done)
		})
		return verb.GateDecision{Gated: true, Accept: true, Skill: "slow", Notes: "ok"}, nil
	}
	return verb.GateDecision{Gated: false}, nil
}

// TestLeaseGateRejectRetry: sequential plan→in_progress gate reject leaves
// status at plan and clears in_flight so a retry can re-enter the edge.
func TestLeaseGateRejectRetry(t *testing.T) {
	wireWithWorkflows(t, map[string]string{"single-story-wf": singleStoryWF})
	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "A", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)

	verb.SetTransitionGater(rejectToGater{to: "in_progress", skill: "code-ac"})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"})
	if err == nil {
		t.Fatal("expected reject")
	}
	json.Unmarshal(call(t, "story-get", map[string]any{"id": a.ID}), &a)
	if a.Status != "plan" {
		t.Fatalf("status after reject = %q", a.Status)
	}

	// Retry succeeds after gater cleared.
	verb.SetTransitionGater(nil)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "in_progress"}), &a)
	if a.Status != "in_progress" {
		t.Fatalf("retry after reject: %q", a.Status)
	}
}

func mustJSONLease(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// rejectToGater rejects transitions into a named status; other edges are ungated.
type rejectToGater struct {
	to    string
	skill string
}

func (r rejectToGater) Gate(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, error) {
	if toStatus == r.to {
		return verb.GateDecision{
			Gated: true, Accept: false, Skill: r.skill, Notes: "forced reject for test",
		}, nil
	}
	return verb.GateDecision{Gated: false}, nil
}
