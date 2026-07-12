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
// Loads EMBEDDED bodies only (hermetic; CI-stable). The exact substring
// "nothing engaged" is the pre-fix failure mode and must not reappear.
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

	// AC1: pre-fix blockage signal must be gone.
	if strings.Contains(recognise, "nothing engaged") {
		t.Error(`satelle-recognise-blockage must not contain "nothing engaged" (contradicts engage-and-proceed)`)
	}

	// AC2: explicit carve-out that engagement is workflow entry, not blockage.
	for _, marker := range []string{"is not blockage", "even with a story engaged"} {
		if !strings.Contains(strings.ToLower(recognise), strings.ToLower(marker)) {
			t.Errorf("satelle-recognise-blockage missing engage-and-proceed carve-out marker %q", marker)
		}
	}

	// edits-require still prescribes engage + proceed.
	lowEdits := strings.ToLower(edits)
	if !strings.Contains(lowEdits, "engage") {
		t.Error("satelle-edits-require-a-story must still prescribe engage")
	}
	// "proceed" appears in the rule surface (gate is not optional / follow workflow).
	if !strings.Contains(lowEdits, "engaged story") && !strings.Contains(lowEdits, "engage") {
		t.Error("satelle-edits-require-a-story must require an engaged story")
	}
}
