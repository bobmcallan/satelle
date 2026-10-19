package remotedef

import (
	"strings"
	"testing"
)

// fiveTriggers is one [[trigger]] schema carrying every kind the model names.
// The kinds are opaque to the engine, so they live here as data.
const fiveTriggers = `[[trigger]]
name = "nightly"
kind = "schedule"
[trigger.params]
cron = "0 2 * * *"

[[trigger]]
name = "new-story"
kind = "story-event"
[trigger.params]
event = "story.created"

[[trigger]]
name = "epic-drive"
kind = "handoff"

[[trigger]]
name = "on-push"
kind = "inbound-event"
[trigger.params]
event = "push"

[[trigger]]
name = "from-session"
kind = "mcp"
`

// withTriggers replaces the scaffold's one trigger with block.
func withTriggers(t *testing.T, block string) string {
	t.Helper()
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", "[[trigger]]\nname = \"manual\"\nkind = \"manual\"\n", block)
	return dir
}

func TestTriggerKindsShareSchema(t *testing.T) {
	dir := withTriggers(t, fiveTriggers)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("five kinds in one schema must validate clean, got:\n%s", strings.Join(details(fs), "\n"))
	}
	def, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, tr := range def.Triggers {
		kinds = append(kinds, tr.Kind)
	}
	if got, want := strings.Join(kinds, ","), "schedule,story-event,handoff,inbound-event,mcp"; got != want {
		t.Errorf("kinds = %s, want %s", got, want)
	}

	for _, c := range []struct {
		name, block, key string
	}{
		{"missing name", "[[trigger]]\nkind = \"mcp\"\n", "trigger[0].name"},
		{"duplicate name", "[[trigger]]\nname = \"a\"\nkind = \"mcp\"\n\n[[trigger]]\nname = \"a\"\nkind = \"schedule\"\n", "trigger[1].name"},
		{"malformed name", "[[trigger]]\nname = \"Not Ok\"\nkind = \"mcp\"\n", "trigger[0].name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs := Validate(withTriggers(t, c.block))
			if !hasFinding(fs, "remote.toml", c.key) {
				t.Fatalf("want a finding for remote.toml %s, got:\n%s", c.key, strings.Join(details(fs), "\n"))
			}
		})
	}
}
