package ledger

import (
	"encoding/json"
	"testing"
)

// TestEntryAgentAlias proves an Entry decodes both the canonical "agent" key and
// the legacy "actor" alias for the performer field (sty_536f9960): a legacy row
// reads back via "actor", a new payload via "agent", and "agent" wins when both
// are present. Emission is unchanged — still "actor".
func TestEntryAgentAlias(t *testing.T) {
	var legacy Entry
	if err := json.Unmarshal([]byte(`{"id":"evt_1","kind":"k","actor":"reviewer"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Actor != "reviewer" {
		t.Errorf("legacy actor read back = %q, want reviewer", legacy.Actor)
	}

	var modern Entry
	if err := json.Unmarshal([]byte(`{"id":"evt_2","kind":"k","agent":"executor"}`), &modern); err != nil {
		t.Fatal(err)
	}
	if modern.Actor != "executor" {
		t.Errorf("agent key read into Actor = %q, want executor", modern.Actor)
	}

	var both Entry
	if err := json.Unmarshal([]byte(`{"id":"evt_3","kind":"k","agent":"executor","actor":"reviewer"}`), &both); err != nil {
		t.Fatal(err)
	}
	if both.Actor != "executor" {
		t.Errorf("agent should win over actor, got %q", both.Actor)
	}

	// Emission is still the legacy "actor" key in this slice.
	out, err := json.Marshal(Entry{ID: "evt_4", Kind: "k", Actor: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || !contains(string(out), `"actor":"executor"`) {
		t.Errorf("emission should keep the actor key, got %s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
