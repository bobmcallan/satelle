package wfdot

import (
	"strings"
	"testing"
)

// solidsafe-style misuse: gate-specific reviewer as single-state on= node
// while the state also has a rework inbound (integration → in_progress).
const warnDot = "```dot\n" + `digraph g {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done [shape=Msquare]
  badgate [agent=reviewer, prompt="@skill:my-gate", on="in_progress"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:intent"]
  in_progress -> integration [agent=reviewer, prompt="@skill:code-ac"]
  integration -> done
  integration -> in_progress
}
` + "```\n"

// Multi-state on= is the legit always-on pattern (estimate) — no warn.
const multiStateDot = "```dot\n" + `digraph g {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done"]
  backlog -> in_progress
  in_progress -> done
  done -> in_progress
}
` + "```\n"

// on="*" always-on — no warn.
const starDot = "```dot\n" + `digraph g {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  always [agent=reviewer, prompt="@skill:always-gate", on="*"]
  backlog -> in_progress
  in_progress -> done
  done -> in_progress
}
` + "```\n"

// Single inbound into the on= state — no warn (cannot re-fire on rework).
const singleInboundDot = "```dot\n" + `digraph g {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  gate [agent=reviewer, prompt="@skill:once-gate", on="in_progress"]
  backlog -> in_progress
  in_progress -> done
}
` + "```\n"

// Step node is never warned (summariser, not a blocking gate).
const stepDot = "```dot\n" + `digraph g {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  step [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  backlog -> in_progress
  in_progress -> done
  done -> in_progress
}
` + "```\n"

func mustParse(t *testing.T, body string) Spec {
	t.Helper()
	s, ok := Parse(body)
	if !ok {
		t.Fatal("Parse failed")
	}
	return s
}

func TestOverFireWarnings_Warn(t *testing.T) {
	w := OverFireWarnings(mustParse(t, warnDot))
	if len(w) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(w), w)
	}
	got := w[0]
	for _, needle := range []string{"badgate", "on=\"in_progress\"", "rework", "bind it to the edge", "satelle help workflows"} {
		if !strings.Contains(got, needle) {
			t.Errorf("warning missing %q: %s", needle, got)
		}
	}
}

func TestOverFireWarnings_NoWarn(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"multi-state", multiStateDot},
		{"on-star", starDot},
		{"single-inbound", singleInboundDot},
		{"step-node", stepDot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := OverFireWarnings(mustParse(t, tc.body))
			if len(w) != 0 {
				t.Fatalf("want no warnings, got %v", w)
			}
		})
	}
}

func TestOverFireWarnings_NonFatalShape(t *testing.T) {
	// Warnings are plain strings for the CLI to print as WARN; they must not
	// look like FAIL lines (validate must stay exit 0).
	w := OverFireWarnings(mustParse(t, warnDot))
	if len(w) == 0 {
		t.Fatal("expected a warning")
	}
	if strings.HasPrefix(w[0], "FAIL") {
		t.Errorf("warning must not be a FAIL line: %s", w[0])
	}
}
