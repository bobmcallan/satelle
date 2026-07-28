package config

import (
	"strings"
	"testing"
)

// TestRecogniseBlockageConsistentWithEditsRequire pins the resident-principle
// invariant that birthed epic:substrate-convergence (sty_0334d12b):
// recognise-blockage must not treat a missing engagement as blockage while
// edits-require-a-story prescribes engage-and-proceed.
//
// Surviving pin (sty_6830e78e AC6): the negative "nothing engaged" phrase is a
// cross-artifact semantic contradiction — no structural property of the
// conformance table can express it. Positive engage markers retired into
// ordinary product reading; only the pre-fix failure mode is pinned.
func TestRecogniseBlockageConsistentWithEditsRequire(t *testing.T) {
	embedded := embeddedPrinciples()

	recognise, ok := embedded["satelle-recognise-blockage"]
	if !ok || recognise == "" {
		t.Fatal("satelle-recognise-blockage must be embedded (AC3)")
	}
	edits, ok := embedded["satelle-edits-require-a-story"]
	if !ok || edits == "" {
		t.Fatal("satelle-edits-require-a-story must be embedded")
	}

	// Pre-fix blockage signal must stay gone.
	if strings.Contains(recognise, "nothing engaged") {
		t.Error(`satelle-recognise-blockage must not contain "nothing engaged" (contradicts engage-and-proceed)`)
	}

	// Minimal existence: edits-require still talks about engagement.
	if !strings.Contains(strings.ToLower(edits), "engage") {
		t.Error("satelle-edits-require-a-story must still prescribe engage")
	}

	// sty_7b69954a AC3: preemption is named separately — not by widening "blockage".
	for _, want := range []string{
		"Preemption is not blockage",
		"stop-request",
		"Never cancel a healthy story to free the seat",
		"preempted-by:",
	} {
		if !strings.Contains(recognise, want) {
			t.Errorf("satelle-recognise-blockage missing preemption guidance %q", want)
		}
	}
	if !strings.Contains(recognise, "Cancelling a healthy story to free the engagement seat") {
		t.Error("satelle-recognise-blockage anti-patterns must name cancel-to-free-seat")
	}
}
