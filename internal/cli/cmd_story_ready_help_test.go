package cli

import (
	"strings"
	"testing"
)

// TestStoryReadyHelpStatesTheOneAccount (sty_87403c0e): a round changes no status,
// and what an epic round does to its children follows the step's ready_children.
func TestStoryReadyHelpStatesTheOneAccount(t *testing.T) {
	long := strings.Join(strings.Fields(storyReadyCommand().Long), " ")
	for _, want := range []string{
		"A round never changes status",
		"stays in backlog",
		"'satelle story set <id> --status plan' takes backlog → plan",
		"with ready_children unset or true it also runs each backlog child's round",
		"With ready_children = false each child is planned when 'satelle story wave' lets it start",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("story ready help missing %q", want)
		}
	}
}
