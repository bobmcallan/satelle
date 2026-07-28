package verb_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// TestSeatStopVerbCatalogPinned (sty_7b69954a AC9): freeing a seat you do not
// own requires either stop-request arbitration (holder consents by parking) or
// the operator's explicit story-seat-release override. No fourth seat-freeing
// verb may appear without updating this guard deliberately.
func TestSeatStopVerbCatalogPinned(t *testing.T) {
	want := map[string]bool{
		"story-seat-list":    true,
		"story-seat-release": true,
		"story-stop-request": true,
	}
	got := map[string]bool{}
	for _, name := range verb.Catalog() {
		if strings.Contains(name, "seat") || strings.Contains(name, "stop") {
			got[name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("expected seat/stop verb %q registered", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected seat/stop verb %q — free a seat you do not own only via stop-request arbitration or operator seat release (sty_7b69954a AC9)", name)
		}
	}
}
