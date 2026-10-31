package config

import (
	"strings"
	"testing"
)

// TestEmbeddedDefaultsAuthorCriteriaAtPlanning: a story may enter as a
// behaviour with no criteria. The embedded defaults state that its criteria are
// authored at planning where the route has a plan step, and that the card is the
// whole spec where it has none; the embedded begin-work intent review judges the
// plan's section instead of rejecting a story for missing criteria.
func TestEmbeddedDefaultsAuthorCriteriaAtPlanning(t *testing.T) {
	embeddedPrincipleShape(t, "satelle-story-shape", []string{
		"A story may enter as a behaviour with no criteria",
		"its criteria are authored at planning",
		"On a route with no plan step, the card is the whole spec",
	})

	var skill string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-story-intent-review" {
			skill = d.Body
		}
	}
	if skill == "" {
		t.Fatal("satelle-story-intent-review must be embedded")
	}
	flat := strings.Join(strings.Fields(skill), " ")
	for _, want := range []string{
		"[[satelle-story-shape]]",
		"criteria are authored at planning",
		"`## Acceptance criteria`",
		"the card is the whole spec",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("satelle-story-intent-review must state %q", want)
		}
	}
	for _, gone := range []string{"ACs are missing", "lists at least one numbered, testable item"} {
		if strings.Contains(flat, gone) {
			t.Errorf("satelle-story-intent-review must not reject a story for missing criteria (%q)", gone)
		}
	}
}
