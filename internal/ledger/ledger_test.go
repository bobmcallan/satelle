package ledger

import (
	"encoding/json"
	"testing"
)

// TestEntryActorIsAuthorField proves the ledger's Actor field is the recorded
// event-AUTHOR, an intentional exemption from the actor→agent rename (sty_7db2ed7d):
// it reads and writes the "actor" JSON key, and the workflow-performer "agent" key
// is NOT a synonym for it (no alias).
func TestEntryActorIsAuthorField(t *testing.T) {
	var e Entry
	if err := json.Unmarshal([]byte(`{"id":"evt_1","kind":"k","actor":"reviewer"}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Actor != "reviewer" {
		t.Errorf("actor read back = %q, want reviewer", e.Actor)
	}

	// The retired performer key "agent" is NOT an alias for the ledger author field.
	var bogus Entry
	if err := json.Unmarshal([]byte(`{"id":"evt_2","kind":"k","agent":"executor"}`), &bogus); err != nil {
		t.Fatal(err)
	}
	if bogus.Actor != "" {
		t.Errorf("an 'agent' key must not populate the ledger Actor field, got %q", bogus.Actor)
	}

	// Emission uses the "actor" key (the exempted storage name).
	out, err := json.Marshal(Entry{ID: "evt_3", Kind: "k", Actor: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || !contains(string(out), `"actor":"executor"`) {
		t.Errorf("emission should use the actor key, got %s", out)
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
