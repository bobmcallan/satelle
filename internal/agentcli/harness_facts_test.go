package agentcli

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/help"
)

// AC1: the wait bound sits below the shortest cutoff of every configured
// harness. Grok's cutoff is 15s, so any set that names grok is bounded under it.
func TestAgentWaitBound_BelowGrokCutoffWheneverGrokIsConfigured(t *testing.T) {
	for _, set := range [][]string{
		{HarnessGrok},
		{HarnessClaude, HarnessGrok},
		{HarnessGrok, HarnessClaude, HarnessUnknown},
		{HarnessUnknown, HarnessGrok},
	} {
		got := AgentWaitBound(set)
		if got >= 15*time.Second {
			t.Errorf("AgentWaitBound(%v) = %s, want below grok's 15s cutoff", set, got)
		}
		if got <= 0 {
			t.Errorf("AgentWaitBound(%v) = %s, want a positive wait", set, got)
		}
	}
}

// The bound is the MINIMUM across the configured harnesses, less the margin.
func TestAgentWaitBound_TakesMinimumAcrossHarnesses(t *testing.T) {
	claudeOnly := AgentWaitBound([]string{HarnessClaude})
	if want := FactsFor(HarnessClaude).BackgroundCutoff - GateWaitMargin; claudeOnly != want {
		t.Errorf("claude alone = %s, want %s", claudeOnly, want)
	}
	both := AgentWaitBound([]string{HarnessClaude, HarnessGrok})
	if want := FactsFor(HarnessGrok).BackgroundCutoff - GateWaitMargin; both != want {
		t.Errorf("claude+grok = %s, want grok's %s (the shortest cutoff wins)", both, want)
	}
	if both >= claudeOnly {
		t.Errorf("adding grok did not shorten the bound: %s vs %s", both, claudeOnly)
	}
}

// Nothing configured, or nothing recognised, gets the conservative floor — never
// a generous default (satelle-agent-agnostic §3).
func TestAgentWaitBound_UnknownHarnessGetsTheFloor(t *testing.T) {
	want := DefaultBackgroundCutoff - GateWaitMargin
	for _, set := range [][]string{nil, {}, {HarnessUnknown}, {"some-new-cli"}} {
		if got := AgentWaitBound(set); got != want {
			t.Errorf("AgentWaitBound(%v) = %s, want the floor %s", set, got, want)
		}
	}
}

// HandoffNeeded is what the `[gate] handoff = "auto"` default rests on
// (sty_8ee31f26): true when any harness in play has a cutoff below a gate's
// foreground budget — grok yes, claude no — and true for nothing configured or
// nothing recognised, because an unrecognised harness gets the floor (§3).
func TestHandoffNeeded(t *testing.T) {
	cases := []struct {
		set  []string
		want bool
	}{
		{[]string{HarnessGrok}, true},
		{[]string{HarnessClaude}, false},
		{[]string{HarnessClaude, HarnessGrok}, true},
		{nil, true},
		{[]string{}, true},
		{[]string{HarnessUnknown}, true},
		{[]string{"some-new-cli"}, true},
		{[]string{HarnessClaude, "some-new-cli"}, true},
	}
	for _, c := range cases {
		if got := HandoffNeeded(c.set); got != c.want {
			t.Errorf("HandoffNeeded(%v) = %v, want %v", c.set, got, c.want)
		}
	}
	// The documented reason must hold: every recorded cutoff either sits below
	// the budget (hand off) or at/above it (do not).
	if DefaultBackgroundCutoff >= GateForegroundBudget {
		t.Errorf("the unknown-harness floor %s must stay below the budget %s", DefaultBackgroundCutoff, GateForegroundBudget)
	}
}

// Every harness row is a value or an adapter-named reason — never a silent
// zero — and an unrecognised harness is named, not assumed to be claude.
func TestHarnessFacts_EveryRowIsExplicit(t *testing.T) {
	for _, f := range HarnessFactsTable() {
		if f.BackgroundCutoff <= 0 {
			t.Errorf("%s: no background cutoff", f.Harness)
		}
		if strings.TrimSpace(f.CutoffBasis) == "" {
			t.Errorf("%s: cutoff carries no basis", f.Harness)
		}
		c := f.CompletionNotification
		if c.Available && c.Reason != "" {
			t.Errorf("%s: available but carries a reason %q", f.Harness, c.Reason)
		}
		if !c.Available && !strings.HasPrefix(c.Reason, f.Harness+":") {
			t.Errorf("%s: unavailable without an adapter-named reason: %q", f.Harness, c.Reason)
		}
	}
	u := FactsFor("mystery")
	if u.CompletionNotification.Available {
		t.Errorf("an unrecognised harness claims a completion notification: %+v", u)
	}
	if !strings.HasPrefix(u.CompletionNotification.Reason, "mystery:") {
		t.Errorf("unrecognised harness reason does not name it: %q", u.CompletionNotification.Reason)
	}
}

// AC3: a harness is recorded as delivering a completion notification only after
// a live run showed it. Grok was observed (sty_c4b92c9e, grok-dogfood-run-1: the
// Stop hook's block reason woke a headless grok 1.0.41 session with the
// verdict); a harness with no row makes no such claim.
func TestHarnessFacts_RecordsObservedWakesAndNamesTheRest(t *testing.T) {
	for _, h := range []string{HarnessClaude, HarnessGrok} {
		if !FactsFor(h).CompletionNotification.Available {
			t.Errorf("%s was observed waking its session on a Stop-hook block; the table must say so", h)
		}
	}
	if FactsFor("nosuch").CompletionNotification.Available {
		t.Error("a harness with no row claims a completion notification nothing wired")
	}
}

// The help topic carries exactly the table the code renders.
func TestHarnessFacts_HelpTopicMatchesCode(t *testing.T) {
	top, ok := help.Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch help topic missing")
	}
	if want := HarnessFactsTableMarkdown(); !strings.Contains(top.Body, want) {
		t.Errorf("help agent-dispatch does not contain the harness-facts table the code renders; want:\n%s", want)
	}
}
