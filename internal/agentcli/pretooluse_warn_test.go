package agentcli

import (
	"strings"
	"testing"
)

// PreToolUse warn encodings (sty_a74551d6): a warn rule's answer is delivered
// only where the harness's PreToolUseWarn cell is yes; every other harness gets
// nothing from the adapter, and its cell names the adapter in a reason.
func TestPreToolUseWarnPerHarness(t *testing.T) {
	for _, h := range []string{HarnessClaude, HarnessGrok, HarnessPi, HarnessCursor, HarnessUnknown, ""} {
		facts := FactsFor(h)
		out := PreToolUseWarn(h, "careful")
		match := PreToolUseWarnMatch(h)
		if facts.PreToolUseWarn.Available {
			if h != HarnessClaude {
				t.Errorf("%s: only claude is recorded as delivering a PreToolUse warn", h)
			}
			if !strings.Contains(string(out), `"additionalContext":"careful"`) || strings.Contains(string(out), "permissionDecision") {
				t.Errorf("%s: warn = %s", h, out)
			}
			if match == "" {
				t.Errorf("%s: delivers a warn but has no wrapper match", h)
			}
			continue
		}
		if out != nil || match != "" {
			t.Errorf("%s: cannot deliver a warn but encodes %q / matches %q", h, out, match)
		}
		name := h
		if name == "" {
			name = HarnessUnknown
		}
		if !strings.HasPrefix(facts.PreToolUseWarn.Reason, name+":") {
			t.Errorf("%s: reason %q is not adapter-named", h, facts.PreToolUseWarn.Reason)
		}
	}
}

func TestPreToolUseWarnCellsRendered(t *testing.T) {
	table := HarnessFactsTableMarkdown()
	for _, f := range HarnessFactsTable() {
		if !strings.Contains(table, f.PreToolUseWarn.Cell()) {
			t.Errorf("%s: rendered facts table lacks the cell %q", f.Harness, f.PreToolUseWarn.Cell())
		}
	}
}

// Deny encodings are unchanged for every harness that has one.
func TestPreToolUseDenyStillPerHarness(t *testing.T) {
	for _, h := range []string{HarnessClaude, HarnessGrok, HarnessPi, HarnessCursor} {
		out := string(PreToolUseDeny(h, "no"))
		if !strings.Contains(out, "deny") || !strings.Contains(out, "no") {
			t.Errorf("%s: deny = %s", h, out)
		}
	}
}
