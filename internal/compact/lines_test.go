package compact

import (
	"strings"
	"testing"
)

func TestFoldRepeatsRoundTrip(t *testing.T) {
	in := "a\nb\nb\nb\nb\nc\nd\nd\nd\ne"
	folded := FoldRepeats(in, 0)
	if !strings.Contains(folded, "... (repeated 4 times)") {
		t.Fatalf("FoldRepeats did not collapse the 4-run: %q", folded)
	}
	if !strings.Contains(folded, "... (repeated 3 times)") {
		t.Fatalf("FoldRepeats did not collapse the 3-run: %q", folded)
	}
	if got := UnfoldRepeats(folded); got != in {
		t.Fatalf("UnfoldRepeats(FoldRepeats(x)) = %q, want %q", got, in)
	}
}

func TestFoldRepeatsBelowMinLeftAlone(t *testing.T) {
	in := "a\nb\nb\nc"
	if got := FoldRepeats(in, 0); got != in {
		t.Fatalf("FoldRepeats collapsed a run below the default min: %q", got)
	}
}

func TestFoldRepeatsCustomMin(t *testing.T) {
	in := "x\nx\ny"
	if got := FoldRepeats(in, 2); !strings.Contains(got, "repeated 2 times") {
		t.Fatalf("FoldRepeats(min=2) did not collapse a 2-run: %q", got)
	}
}

func TestStripANSIRemovesEscapes(t *testing.T) {
	in := "\x1b[31mred\x1b[0m plain"
	got := StripANSI(in)
	if got != "red plain" {
		t.Fatalf("StripANSI(%q) = %q, want %q", in, got, "red plain")
	}
}

func TestStripANSINoOpOnPlainText(t *testing.T) {
	in := `{"a":1,"b":"two"}`
	if got := StripANSI(in); got != in {
		t.Fatalf("StripANSI(plain JSON) = %q, want unchanged", got)
	}
}
