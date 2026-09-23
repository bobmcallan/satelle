package retrieve

import "testing"

func TestMarkerAndSummaryDetectedByMarkerRE(t *testing.T) {
	hash := Hash([]byte("hello world"))

	marker := Marker(hash)
	if got := FindHashes(marker); len(got) != 1 || got[0] != hash {
		t.Fatalf("FindHashes(marker) = %v, want [%s]", got, hash)
	}

	summary := Summary(hash, 120, 8)
	if got := FindHashes(summary); len(got) != 1 || got[0] != hash {
		t.Fatalf("FindHashes(summary) = %v, want [%s]", got, hash)
	}

	both := marker + "\n" + summary
	if got := FindHashes(both); len(got) != 1 || got[0] != hash {
		t.Fatalf("FindHashes(both forms, same hash) = %v, want deduped [%s]", got, hash)
	}
}

func TestMarkerRENoFalsePositives(t *testing.T) {
	for _, s := range []string{
		"",
		"plain text with no markers",
		"<<ccr:tooshort>>",                 // not 24 hex chars
		"<<ccr:ZZZZZZZZZZZZZZZZZZZZZZZZ>>", // not hex
		"Retrieve more: satelle retrieve not-a-hash",
	} {
		if got := FindHashes(s); len(got) != 0 {
			t.Errorf("FindHashes(%q) = %v, want none", s, got)
		}
	}
}

func TestFindHashesMultipleDistinct(t *testing.T) {
	h1 := Hash([]byte("a"))
	h2 := Hash([]byte("b"))
	s := Marker(h1) + " and " + Marker(h2)
	got := FindHashes(s)
	if len(got) != 2 || got[0] != h1 || got[1] != h2 {
		t.Fatalf("FindHashes(two markers) = %v, want [%s %s]", got, h1, h2)
	}
}
