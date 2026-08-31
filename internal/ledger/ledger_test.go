package ledger

import (
	"encoding/json"
	"strings"
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

// TestNewIDShapeSurvivesTheGenerator (sty_5515036d): the id keeps only the first
// 8 hex characters of a UUID, so those bits must be RANDOM. This pins the shape
// and the randomness across the swap from github.com/google/uuid to the Go 1.27
// stdlib package — a v7 UUID leads with a millisecond timestamp, and truncating
// one would collide for every id minted in the same instant.
func TestNewIDShapeSurvivesTheGenerator(t *testing.T) {
	const draws = 2000
	seen := make(map[string]bool, draws)
	for i := 0; i < draws; i++ {
		id := NewID()
		rest, ok := strings.CutPrefix(id, "evt_")
		if !ok || len(rest) != 8 {
			t.Fatalf("id %q is not evt_<8 chars>", id)
		}
		for _, r := range rest {
			if !strings.ContainsRune("0123456789abcdef", r) {
				t.Fatalf("id %q carries a non lowercase-hex character %q", id, r)
			}
		}
		seen[id] = true
	}
	// A timestamp-leading generator would repeat heavily within one run.
	if len(seen) < draws-1 {
		t.Errorf("%d distinct ids from %d draws — the truncated prefix is not random enough", len(seen), draws)
	}
}
