package runstatus

import "testing"

func TestKeyTellsConclusionsApart(t *testing.T) {
	base := Run{ID: "nod_1", State: Concluded, Outcome: "reject", Findings: "missing test"}
	if base.Key() != base.Key() {
		t.Fatal("a conclusion's key is stable")
	}
	for name, other := range map[string]Run{
		"another outcome":    {ID: "nod_1", State: Concluded, Outcome: "accept", Findings: "missing test"},
		"other findings":     {ID: "nod_1", State: Concluded, Outcome: "reject", Findings: "still missing"},
		"another id":         {ID: "nod_2", State: Concluded, Outcome: "reject", Findings: "missing test"},
		"another conclusion": {ID: "nod_1", ConcludedAt: "2026-10-24T02:00:00Z", State: Concluded, Outcome: "reject", Findings: "missing test"},
	} {
		if other.Key() == base.Key() {
			t.Errorf("%s: key %q must differ from %q", name, other.Key(), base.Key())
		}
	}
}
