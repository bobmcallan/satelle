package cli

import (
	"context"
	"io"
	"testing"
)

// TestSupervisorTracksFailedChildrenAndNotifies covers the landing's live
// contract (sty_4ea4d4df): a registered project whose child cannot spawn is
// recorded as FAILED (surfaced on the landing, not silently omitted), the
// served-set change doorbells the "projects" topic, and removal clears it.
func TestSupervisorTracksFailedChildrenAndNotifies(t *testing.T) {
	// A self binary that cannot exec — every spawn fails deterministically.
	sup := newSupervisor(context.Background(), io.Discard, io.Discard, "/nonexistent/satelle-binary")
	var topics []string
	sup.notify = func(topic string) { topics = append(topics, topic) }

	repo := t.TempDir()
	sup.reconcile([]string{repo})

	failed := sup.snapshotFailed()
	if len(failed) != 1 || failed[0].Path != repo || failed[0].Err == "" {
		t.Fatalf("failed child not tracked: %+v", failed)
	}
	if len(sup.snapshot()) != 0 {
		t.Errorf("a failed child must not appear as a served project")
	}
	if len(topics) == 0 || topics[0] != "projects" {
		t.Errorf("reconcile should doorbell the projects topic, got %v", topics)
	}

	// Removing the project from the registry clears the failed row and doorbells.
	topics = nil
	sup.reconcile([]string{})
	if len(sup.snapshotFailed()) != 0 {
		t.Errorf("failed row should clear when the project is deregistered")
	}
	if len(topics) == 0 {
		t.Errorf("removal should doorbell the projects topic")
	}

	// A no-change reconcile is quiet (no doorbell churn).
	topics = nil
	sup.reconcile([]string{})
	if len(topics) != 0 {
		t.Errorf("no-change reconcile must not doorbell, got %v", topics)
	}
}
