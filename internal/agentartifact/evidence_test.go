package agentartifact

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEvidenceValid(t *testing.T) {
	body := "---\nname: coder\nevidence_sections: Premise check, \"Existing owner\"\nevidence_keys: [body, name]\nevidence_commands: go test, satelle validate\nevidence_tools: Read\n---\nbody\n"
	got, err := ParseEvidence(body)
	if err != nil {
		t.Fatal(err)
	}
	want := Evidence{
		Sections: []string{"Premise check", "Existing owner"},
		Keys:     []string{"body", "name"},
		Commands: []string{"go test", "satelle validate"},
		Tools:    []string{"Read"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseEvidence = %+v, want %+v", got, want)
	}
	if !got.Declared() {
		t.Error("Declared() = false for a declaring skill")
	}
}

func TestParseEvidenceNoneDeclared(t *testing.T) {
	for _, body := range []string{"no frontmatter", "---\nname: x\n---\nbody"} {
		got, err := ParseEvidence(body)
		if err != nil || got.Declared() {
			t.Errorf("ParseEvidence(%q) = %+v, %v; want zero, nil", body, got, err)
		}
	}
}

func TestParseEvidenceMalformed(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"unknown key":    {"---\nevidence_command: go test\n---\n", "evidence_command"},
		"empty value":    {"---\nevidence_sections:\n---\n", "evidence_sections"},
		"empty item":     {"---\nevidence_commands: go test,,make\n---\n", "evidence_commands"},
		"trailing comma": {"---\nevidence_tools: Read,\n---\n", "evidence_tools"},
		"open bracket":   {"---\nevidence_keys: [body\n---\n", "evidence_keys"},
	} {
		_, err := ParseEvidence(tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
}
