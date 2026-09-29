package help

import (
	"strings"
	"testing"
)

// sty_a7914904 AC7 (satelle-configure-freely: no undocumented rule): the spend
// budget keys, the fresh-session warning, the coder-step edit refusal and the
// overrun consequence are documented in satelle help.
func TestSpendBudgetRulesAreDocumented(t *testing.T) {
	dispatch, ok := Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch help topic missing")
	}
	workflows, ok := Get("workflows")
	if !ok {
		t.Fatal("workflows help topic missing")
	}
	for _, want := range []string{
		"context_budget", "turn_budget", "{max_turns}",
		"start a fresh session", "session_advisory", "other-story", "context-budget",
		"budget_overrun", "satelle story rework", "one-shot dispatch",
		"only warns and records", "no blocked state", "in-loop `executor`",
	} {
		if !strings.Contains(dispatch.Body, want) {
			t.Errorf("help agent-dispatch does not document %q", want)
		}
	}
	for _, want := range []string{"context_budget", "turn_budget"} {
		if !strings.Contains(workflows.Body, want) {
			t.Errorf("help workflows does not document the step key %q", want)
		}
	}
}
