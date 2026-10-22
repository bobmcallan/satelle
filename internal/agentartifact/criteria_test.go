package agentartifact

import "testing"

func TestSectionIsTheTextUnderTheHeading(t *testing.T) {
	doc := "# Plan\n\n## Design\nd\n\n## Acceptance criteria\n1. one\n2. two\n\n## Risks\nr\n"
	if got := Section(doc, "Acceptance criteria"); got != "1. one\n2. two" {
		t.Errorf("Section = %q", got)
	}
	if got := Section(doc, "Missing"); got != "" {
		t.Errorf("an absent heading yields nothing, got %q", got)
	}
	if got := Section("## Acceptance criteria\t\n1. x", "Acceptance criteria"); got != "1. x" {
		t.Errorf("a heading followed by a tab matches, got %q", got)
	}
}

func TestNumberedCriteria(t *testing.T) {
	text := "preamble\n1. first line\n   continues here\n\n2) second\n10. tenth\n2. duplicate\nignored continuation\n"
	got := NumberedCriteria(text)
	want := []Criterion{
		{N: 1, Text: "first line\ncontinues here"},
		{N: 2, Text: "second"},
		{N: 10, Text: "tenth"},
	}
	if len(got) != len(want) {
		t.Fatalf("NumberedCriteria = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("criterion %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if NumberedCriteria("no numbers here") != nil {
		t.Error("text with no numbered line has no criteria")
	}
}
