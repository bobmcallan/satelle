package agentcli

import "testing"

// HarnessOf identifies the harness behind a command for [harness.<name>]
// lookups (sty_f141c77f); it recognises pi, which the reviewer-isolation
// classifier deliberately does not.
func TestHarnessOf(t *testing.T) {
	for command, want := range map[string]string{
		"claude -p {system}":                      HarnessClaude,
		"/opt/bin/claude":                         HarnessClaude,
		"grok agent stdio":                        HarnessGrok,
		"pi -p {system}":                          HarnessPi,
		"/usr/bin/pi-agent --x":                   HarnessPi,
		"/usr/bin/PI":                             HarnessPi,
		"cursor-agent -p":                         HarnessCursor,
		"/home/u/.local/bin/cursor-agent --trust": HarnessCursor,
		"cursor-agent-beta -p":                    HarnessCursor,
		"cursor":                                  HarnessUnknown,
		"agent -p":                                HarnessUnknown,
		"pip install":                             HarnessUnknown,
		"mybot -p":                                HarnessUnknown,
		"pimento":                                 HarnessUnknown,
		"":                                        HarnessUnknown,
		"   ":                                     HarnessUnknown,
		"mybot --engine grok-fast":                HarnessGrok,
	} {
		if got := HarnessOf(command); got != want {
			t.Errorf("HarnessOf(%q) = %q, want %q", command, got, want)
		}
	}
}

// Pinned: the isolation classifier is NOT changed by HarnessOf. pi stays
// unrecognised for reviewer isolation (preflight's no-tool-trim gap, the
// operator-attested path), so the two differ for pi and agree elsewhere.
func TestHarnessOfDoesNotWidenReviewerIsolation(t *testing.T) {
	for _, command := range []string{"pi -p {system}", "/usr/bin/pi-agent"} {
		if got := AdapterName(command); got != HarnessUnknown {
			t.Errorf("AdapterName(%q) = %q, must stay unknown for isolation", command, got)
		}
		if !UnrecognisedCommand(command) {
			t.Errorf("UnrecognisedCommand(%q) must stay true", command)
		}
		if HarnessOf(command) != HarnessPi {
			t.Errorf("HarnessOf(%q) must be pi", command)
		}
	}
	// cursor-agent is recognised by HarnessOf (sty_7d098d50) and, since it became a
	// dispatchable seat (sty_10c52ab3), by the isolation classifier too: its
	// reviewer ceiling is the forced mode (cursor_seat.go), not an attestation. A
	// --model naming another provider does not change that.
	for _, command := range []string{"cursor-agent -p", "/home/u/.local/bin/cursor-agent --trust", "cursor-agent -p --model grok-4.7-high"} {
		if got := AdapterName(command); got != HarnessCursor {
			t.Errorf("AdapterName(%q) = %q, want cursor", command, got)
		}
		if UnrecognisedCommand(command) {
			t.Errorf("UnrecognisedCommand(%q) must be false", command)
		}
		if HarnessOf(command) != HarnessCursor {
			t.Errorf("HarnessOf(%q) must be cursor", command)
		}
	}
	for _, command := range []string{"claude -p", "grok agent", "mybot"} {
		if HarnessOf(command) != AdapterName(command) {
			t.Errorf("HarnessOf and AdapterName must agree for %q", command)
		}
	}
}
