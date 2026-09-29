package agentcli

import (
	"reflect"
	"strings"
	"testing"
)

// sty_a7914904: a repo's turn_budget reaches the harness through {max_turns};
// unset drops the flag; acp cannot carry it and says so by adapter.

func TestBuildArgsMaxTurns(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		n    int
		want []string
	}{
		{"claude command", "-p --output-format json --max-turns {max_turns} --model {model}", 12,
			[]string{"-p", "--output-format", "json", "--max-turns", "12"}},
		{"grok command", "-p {payload} --max-turns {max_turns} --no-subagents", 7,
			[]string{"-p", "", "--max-turns", "7", "--no-subagents"}},
		{"fused", "-c max_turns={max_turns}", 5, []string{"-c", "max_turns=5"}},
		{"unset drops flag", "-p --max-turns {max_turns} --no-subagents", 0,
			[]string{"-p", "--no-subagents"}},
		{"unset drops fused", "-p -c max_turns={max_turns}", 0, []string{"-p"}},
	}
	for _, c := range cases {
		got := buildArgs(strings.Fields(c.tmpl), Request{MaxTurns: c.n})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: buildArgs = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTurnBudgetSupportNamesTheAdapter(t *testing.T) {
	cases := []struct {
		name, iface, cmd string
		want             bool
		reasonHas        string
	}{
		{"claude command with placeholder", "command", "claude -p --max-turns {max_turns}", true, ""},
		{"claude stream with placeholder", "stream", "claude -p --input-format stream-json --max-turns {max_turns}", true, ""},
		{"grok command with placeholder", "", "grok -p {payload} --max-turns {max_turns}", true, ""},
		{"claude command without", "command", "claude -p --output-format json", false, "claude command"},
		{"grok command without", "command", "grok -p {payload}", false, "grok command"},
		{"unrecognised harness without", "command", "my-agent --run", false, "unknown command"},
		{"grok acp", "acp", "grok agent stdio", false, "grok agent stdio"},
		{"in-loop", "command", "in-loop", false, "in-loop"},
	}
	for _, c := range cases {
		got := TurnBudgetSupport(c.iface, c.cmd)
		if got.Available != c.want {
			t.Errorf("%s: Available = %v, want %v (%q)", c.name, got.Available, c.want, got.Reason)
		}
		if !got.Available && !strings.Contains(got.Reason, c.reasonHas) {
			t.Errorf("%s: reason %q must name %q", c.name, got.Reason, c.reasonHas)
		}
	}
}

func TestACPRejectsMaxTurnsPlaceholder(t *testing.T) {
	_, err := newACPRunner("grok agent stdio --max-turns {max_turns}")
	if err == nil || !strings.Contains(err.Error(), "{max_turns}") || !strings.Contains(err.Error(), "grok agent stdio") {
		t.Fatalf("err = %v, want an adapter-named {max_turns} refusal", err)
	}
}

func TestUnwrapUsageReadsNumTurns(t *testing.T) {
	_, u := UnwrapUsage([]byte(`{"result":"ok","num_turns":4,"usage":{"input_tokens":1,"output_tokens":2}}`))
	if !u.TurnsAvailable || u.Turns != 4 {
		t.Errorf("claude envelope: turns = %d available=%v, want 4 true", u.Turns, u.TurnsAvailable)
	}
	_, u = UnwrapUsage([]byte(`{"text":"ok","num_turns":9,"usage":{"input_tokens":1,"output_tokens":2}}`))
	if !u.TurnsAvailable || u.Turns != 9 {
		t.Errorf("grok envelope: turns = %d available=%v, want 9 true", u.Turns, u.TurnsAvailable)
	}
	_, u = UnwrapUsage([]byte(`{"result":"ok","usage":{"input_tokens":1,"output_tokens":2}}`))
	if u.TurnsAvailable || u.Turns != 0 {
		t.Errorf("an envelope with no num_turns must read unavailable, never zero: %+v", u)
	}
	if !strings.Contains(TurnsUnavailableReason("command", "claude -p"), "claude command") {
		t.Errorf("unavailable reason must name the adapter")
	}
}
