package ledger

import "testing"

// An adapter-named reason (agentcli.ModelUnavailableFor) is never rendered as a
// measured model id (sty_8e422d47).
func TestModelLabel_AdapterReasonIsNotAnID(t *testing.T) {
	if got := ModelLabel("grok-4.5", "unavailable: grok acp reports no model"); got != "grok-4.5 (unknown)" {
		t.Errorf("label = %q, want grok-4.5 (unknown)", got)
	}
	if got := ModelLabel("", "unavailable: codex command reports no model"); got != "unknown" {
		t.Errorf("label = %q, want unknown", got)
	}
	if got := ModelLabel("grok-4.5", "grok-4.7"); got != "grok-4.7" {
		t.Errorf("label = %q, want the measured id", got)
	}
}
