package agentcli

import (
	"strings"
	"testing"
)

// maxTurnsArg returns the value after --max-turns in the argv a template builds.
func maxTurnsArg(t *testing.T, tmpl string, req Request) string {
	t.Helper()
	args := buildArgs(strings.Fields(tmpl)[1:], req)
	for i, a := range args {
		if a == "--max-turns" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("no --max-turns in argv %q", args)
	return ""
}

// The grok preset seeds a reviewer with a turn cap that fits a code-verifying
// review (the measured worst case is 20 turns) and every other role with the
// unchanged original, both still bounded.
func TestGrokDefaultTurnBudgetByRole(t *testing.T) {
	cases := []struct {
		name, tmpl, want string
	}{
		{"reviewer", GrokCommandFor("reviewer"), "32"},
		{"reviewer constant", DefaultGrokReviewerCommand, "32"},
		{"agent", GrokCommandFor("agent"), "16"},
		{"empty role", GrokCommandFor(""), "16"},
		{"default constant", DefaultGrokCommand, "16"},
	}
	for _, c := range cases {
		if got := maxTurnsArg(t, c.tmpl, Request{}); got != c.want {
			t.Errorf("%s: --max-turns %s, want %s", c.name, got, c.want)
		}
	}
	if strings.ReplaceAll(DefaultGrokReviewerCommand, "--max-turns 32", "--max-turns 16") != DefaultGrokCommand {
		t.Error("reviewer and agent grok templates must differ only in --max-turns")
	}
}

// A binding that authors its own turn limit runs with that value unchanged.
func TestBindingOwnTurnLimitIsUnchanged(t *testing.T) {
	if got := maxTurnsArg(t, "grok -p {payload} --max-turns 8", Request{}); got != "8" {
		t.Errorf("literal override = %s, want 8", got)
	}
	if got := maxTurnsArg(t, "grok -p {payload} --max-turns {max_turns}", Request{MaxTurns: 40}); got != "40" {
		t.Errorf("placeholder with turn_budget = %s, want 40", got)
	}
}

func TestEffectiveTurnBudget(t *testing.T) {
	cases := []struct {
		name, iface, cmd string
		configured       int
		want             string
	}{
		{"grok reviewer default", "command", DefaultGrokReviewerCommand, 0, "turn budget: 32 (command --max-turns)"},
		{"grok agent default", "command", DefaultGrokCommand, 0, "turn budget: 16 (command --max-turns)"},
		{"literal wins over unrelated budget", "command", "grok -p {payload} --max-turns 8", 99, "turn budget: 8 (command --max-turns)"},
		{"fused literal", "command", "grok --max-turns=9", 0, "turn budget: 9 (command --max-turns)"},
		{"placeholder with budget", "command", "grok --max-turns {max_turns}", 40, "turn budget: 40 (turn_budget)"},
		{"claude stream placeholder with budget", "stream", "claude -p --max-turns {max_turns}", 12, "turn budget: 12 (turn_budget)"},
		{"placeholder without budget", "command", "grok --max-turns {max_turns}", 0, "turn budget: none (grok command: no turn_budget set)"},
		{"claude no flag", "command", DefaultClaudeCommand, 5, "turn budget: none (claude command: the command template carries no {max_turns} placeholder"},
		{"grok acp", "acp", "grok agent stdio", 5, "turn budget: none (" + acpTurnBudgetReason + ")"},
		{"in-loop", "command", "in-loop", 5, "turn budget: none (in-loop:"},
		{"unknown harness", "command", "mystery --max-turns 3", 0, "turn budget: 3 (command --max-turns)"},
		{"cloud", "cloud", "grok", 5, "turn budget: none (grok cloud:"},
	}
	for _, c := range cases {
		got := EffectiveTurnBudget(c.iface, c.cmd, c.configured)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: EffectiveTurnBudget = %q, want prefix %q", c.name, got, c.want)
		}
	}
}
