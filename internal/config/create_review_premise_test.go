package config

import (
	"strings"
	"testing"
)

// TestCreateReviewPremiseFalsification (sty_b9692e1f): the embedded create gate
// carries the premise-falsification rule, bounded against opinion, and aligned
// with plan-review falsification (cross-link, not a second discipline).
func TestCreateReviewPremiseFalsification(t *testing.T) {
	var body string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-story-create-review" {
			body = d.Body
			break
		}
	}
	if body == "" {
		t.Fatal("embedded create-review skill missing")
	}
	// Rule present
	for _, want := range []string{
		"Premise",
		"falsif",
		"about this repo",
		"contradict",
		"file/symbol",
		"Never reject",
		"worthwhile",
		"satelle-story-plan-review",
	} {
		if !strings.Contains(body, want) && !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("create-review missing load-bearing phrase %q", want)
		}
	}
	// Opinion is not reject grounds (wording variants)
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "never reject") && !strings.Contains(lower, "never grounds") {
		t.Error("must bound the rule against opinion rejects")
	}
	if !strings.Contains(lower, "out of scope") || !strings.Contains(lower, "priority") {
		t.Error("must name non-falsifiable claim classes (value/priority/future)")
	}
	// Sibling alignment — one concept cross-linked
	if !strings.Contains(body, "[[satelle-story-plan-review]]") && !strings.Contains(body, "satelle-story-plan-review") {
		t.Error("must cross-link plan-review falsification rather than invent a second discipline")
	}
}
