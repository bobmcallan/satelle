package config

import (
	"strings"
	"testing"
)

// TestCreateReviewBehaviorRule (sty_37c762f5): the embedded create gate requires
// stated behavior that holds whichever implementation produces it, restates it
// on accept, and treats a recorded solution beside that behavior as context.
func TestCreateReviewBehaviorRule(t *testing.T) {
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
	for _, want := range []string{
		"desired behavior",
		"whichever implementation",
		"name the behavior that is missing",
		"open with `Behavior:`",
		"is context",
		"only a solution",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("create-review missing behavior-rule phrase %q", want)
		}
	}
	for _, gone := range []string{
		"a mechanism the draft says must be built",
		"Not a prescription",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("create-review still carries the retired reject-on-any-mechanism wording %q", gone)
		}
	}
}

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
