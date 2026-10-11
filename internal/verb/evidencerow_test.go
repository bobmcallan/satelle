package verb_test

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// TestIsEvidenceRow: the one rule for "is this ledger row evidence". The rework
// relay's cc="*" transcript is conversation, not work; a directed message, a
// driver story log and an attachment count, and the binary's self-reports sort
// the way the staleness rule has always sorted them.
func TestIsEvidenceRow(t *testing.T) {
	msg := func(cc string) ledger.Entry {
		raw, _ := json.Marshal(verb.AgentMessage{From: "a", To: "b", Cc: cc, Body: "x"})
		return ledger.Entry{Kind: ledger.KindAgentMessage, Payload: raw}
	}
	tel := func(kind string) ledger.Entry {
		raw, _ := json.Marshal(telemetryKind(kind))
		return ledger.Entry{Kind: ledger.KindTelemetryEvent, Payload: raw}
	}
	for _, tc := range []struct {
		name string
		row  ledger.Entry
		want bool
	}{
		{"relay transcript (cc *)", msg("*"), false},
		{"directed message (no cc)", msg(""), true},
		{"driver story log", tel("plan-consumed"), true},
		{"engine telemetry", tel("agent-attempt"), false},
		{"tool permission", ledger.Entry{Kind: ledger.KindToolPermission}, false},
		{"agent invocation", ledger.Entry{Kind: ledger.KindAgentInvocation}, false},
		{"review verdict", ledger.Entry{Kind: ledger.KindReviewReject}, false},
		{"attachment", ledger.Entry{Kind: verb.KindStoryDocAttached}, true},
	} {
		if got := verb.IsEvidenceRow(tc.row); got != tc.want {
			t.Errorf("%s: IsEvidenceRow = %t, want %t", tc.name, got, tc.want)
		}
	}
}
