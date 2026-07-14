package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// TestSupervisorTracksFailedChildrenAndNotifies covers the landing's live
// contract (sty_4ea4d4df + sty_5faf46f1): a registered project whose child
// cannot spawn is recorded as FAILED (surfaced on the landing, not silently
// omitted), the served-set change doorbells the "projects" topic, and removal
// clears it. Spawn now runs asynchronously in supervise — poll for the failure.
func TestSupervisorTracksFailedChildrenAndNotifies(t *testing.T) {
	// A self binary that cannot exec — every spawn fails deterministically.
	sup := newSupervisor(context.Background(), io.Discard, io.Discard, "/nonexistent/satelle-binary")
	var topics []string
	sup.notify = func(topic string) { topics = append(topics, topic) }

	repo := t.TempDir()
	sup.reconcile([]string{repo})

	// supervise marks failed asynchronously after the first spawn attempt.
	deadline := time.Now().Add(3 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		f := sup.snapshotFailed()
		if len(f) == 1 && f[0].Path == repo && f[0].Err != "" {
			ok = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("failed child not tracked: snapshotFailed=%+v", sup.snapshotFailed())
	}
	if len(sup.snapshot()) != 0 {
		t.Errorf("a failed child must not appear as a served project")
	}
	// At least one projects doorbell from markFailed.
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(topics) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(topics) == 0 || topics[0] != "projects" {
		t.Errorf("spawn failure should doorbell the projects topic, got %v", topics)
	}

	// Removing the project from the registry clears the failed row and doorbells.
	topics = nil
	sup.reconcile([]string{})
	// stopManaging notifies; give the goroutine a moment to exit.
	time.Sleep(50 * time.Millisecond)
	if len(sup.snapshotFailed()) != 0 {
		t.Errorf("failed row should clear when the project is deregistered: %+v", sup.snapshotFailed())
	}
	if len(topics) == 0 {
		t.Errorf("removal should doorbell the projects topic")
	}

	// A no-change reconcile is quiet (no doorbell churn).
	topics = nil
	sup.reconcile([]string{})
	time.Sleep(30 * time.Millisecond)
	if len(topics) != 0 {
		t.Errorf("no-change reconcile must not doorbell, got %v", topics)
	}
	sup.shutdown()
}

// TestSpawnRefusesUnhealthyBoot asserts spawn returns an error (and no childProc)
// when the binary cannot start — the root of defect 2 (no dead proxy registration).
func TestSpawnRefusesUnhealthyBoot(t *testing.T) {
	sup := newSupervisor(context.Background(), io.Discard, io.Discard, "/nonexistent/satelle-binary")
	c, err := sup.spawn(context.Background(), t.TempDir(), "broken")
	if err == nil || c != nil {
		t.Fatalf("spawn must error on unstartable binary; got c=%v err=%v", c, err)
	}
}

func TestNextBackoffCaps(t *testing.T) {
	if got := nextBackoff(respawnBackoffBase); got != 500*time.Millisecond {
		t.Errorf("nextBackoff(base) = %v, want 500ms", got)
	}
	if got := nextBackoff(respawnBackoffCap); got != respawnBackoffCap {
		t.Errorf("nextBackoff(cap) = %v, want cap", got)
	}
}

// TestSupervisorParksAfterFastSpawnFailures (sty_5faf46f1 AC2): consecutive
// spawn failures park the project (failed reason names "parked") and the
// supervise loop stops (managing entry cleared).
func TestSupervisorParksAfterFastSpawnFailures(t *testing.T) {
	sup := newSupervisor(context.Background(), io.Discard, io.Discard, "/nonexistent/satelle-binary")
	repo := t.TempDir()
	sup.reconcile([]string{repo})

	// Backoff sum to park: 250+500+1s+2s+4s ≈ 7.75s plus spawn attempts.
	deadline := time.Now().Add(30 * time.Second)
	parked := false
	for time.Now().Before(deadline) {
		for _, f := range sup.snapshotFailed() {
			if f.Path == repo && strings.Contains(f.Err, "parked") {
				parked = true
				break
			}
		}
		if parked {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !parked {
		t.Fatalf("expected parked failed row, got %+v", sup.snapshotFailed())
	}
	// After park the supervise loop exits — not in managing.
	sup.mu.Lock()
	_, still := sup.managing[repo]
	sup.mu.Unlock()
	if still {
		t.Error("parked project must drop managing entry (no hot loop)")
	}
	if len(sup.snapshot()) != 0 {
		t.Error("parked project must not appear healthy")
	}
	sup.shutdown()
}
