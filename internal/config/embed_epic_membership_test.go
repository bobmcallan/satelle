package config

import (
	"strings"
	"testing"
)

// sty_9f4f8e12 AC6/AC8: the epic membership rule is stated once, in
// satelle-story-classification; the goals principle and the done-review skill
// point at it rather than restating a set; the retrospective skill the binary
// ships states the closed-epic behaviour the create path implements.
func TestEmbeddedEpicMembershipHasOneHome(t *testing.T) {
	body := map[string]string{}
	for _, d := range EmbeddedDefaults() {
		body[d.Kind+"/"+d.Name] = d.Body
	}
	const home = "principles/satelle-story-classification"
	rule, ok := body[home]
	if !ok {
		t.Fatalf("%s is not embedded", home)
	}
	for _, want := range []string{"every story carrying `epic:<theme>`", "`parent_id` is not membership", "no\n  `epic-child` category"} {
		if !strings.Contains(rule, want) {
			t.Errorf("%s must state the rule (%q)", home, want)
		}
	}
	for _, name := range []string{"principles/satelle-agent-goals", "skills/satelle-story-done-review"} {
		b, ok := body[name]
		if !ok {
			t.Fatalf("%s is not embedded", name)
		}
		if !strings.Contains(b, "[[satelle-story-classification]]") {
			t.Errorf("%s must point at satelle-story-classification", name)
		}
		if strings.Contains(b, "epic:<theme>") || strings.Contains(b, "parent_id") {
			t.Errorf("%s must not restate the membership rule", name)
		}
	}
	if !strings.Contains(body["skills/satelle-story-done-review"], "snapshot") {
		t.Error("the done-review skill must say the injected children list is a snapshot")
	}
}

func TestEmbeddedRetrospectiveStatesClosedEpicBehaviour(t *testing.T) {
	var found string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-retrospective" {
			found = d.Body
		}
	}
	if found == "" {
		t.Fatal("satelle-retrospective is not embedded")
	}
	for _, want := range []string{"epic:<theme>", "WITHOUT the `epic:` tag", "done or cancelled"} {
		if !strings.Contains(found, want) {
			t.Errorf("the embedded retrospective skill must state the closed-epic behaviour (%q)", want)
		}
	}
}
