package agentcli

import "testing"

// HarnessOf identifies the harness behind a command for [harness.<name>]
// lookups (sty_f141c77f); it recognises pi, which the reviewer-isolation
// classifier deliberately does not.
func TestHarnessOf(t *testing.T) {
	for command, want := range map[string]string{
		"claude -p {system}":       HarnessClaude,
		"/opt/bin/claude":          HarnessClaude,
		"grok agent stdio":         HarnessGrok,
		"pi -p {system}":           HarnessPi,
		"/usr/bin/pi-agent --x":    HarnessPi,
		"/usr/bin/PI":              HarnessPi,
		"pip install":              HarnessUnknown,
		"mybot -p":                 HarnessUnknown,
		"pimento":                  HarnessUnknown,
		"":                         HarnessUnknown,
		"   ":                      HarnessUnknown,
		"mybot --engine grok-fast": HarnessGrok,
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
	for _, command := range []string{"claude -p", "grok agent", "mybot"} {
		if HarnessOf(command) != AdapterName(command) {
			t.Errorf("HarnessOf and AdapterName must agree for %q", command)
		}
	}
}
