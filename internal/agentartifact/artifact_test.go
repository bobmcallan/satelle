package agentartifact

import (
	"strings"
	"testing"
)

const contractedSkill = `---
name: arbitrary-step
type: skill
output_name: design-notes
output_type: design
output_required: true
output_schema: body
output_ac_coverage: true
---
rubric`

func TestParseContractIsGenericAndLegacySafe(t *testing.T) {
	c, err := ParseContract(contractedSkill)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "design-notes" || c.Type != "design" || !c.Required || !c.ACCoverage {
		t.Fatalf("contract = %#v", c)
	}
	legacy, err := ParseContract("---\nname: legacy\ntype: skill\n---\nrubric")
	if err != nil || legacy.Active() {
		t.Fatalf("legacy contract = %#v, err %v", legacy, err)
	}
}

func TestDecodeAndValidateStructuredArtifact(t *testing.T) {
	out := []byte("prose first\n" + `{"artifact":{"body":"## AC1\nproof\n## AC2\nproof"}}`)
	a, err := Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := ParseContract(contractedSkill)
	a, err = Validate(a, c, "1. first\n2. second")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "design-notes" || a.Type != "design" || !strings.Contains(a.Body, "AC2") {
		t.Fatalf("artifact = %#v", a)
	}
}

func TestDecodeFieldErrors(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"not json", "plain text", "no structured"},
		{"artifact not object", `{"artifact":"bad"}`, "artifact: expected object"},
		{"missing body", `{"artifact":{"name":"x"}}`, "artifact.body: required field missing"},
		{"wrong body type", `{"artifact":{"body":7}}`, "artifact.body: expected string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.out))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateReportsMismatchAndMissingCriterion(t *testing.T) {
	c, _ := ParseContract(contractedSkill)
	if _, err := Validate(Artifact{Name: "wrong", Body: "## AC1"}, c, "1. one"); err == nil || !strings.Contains(err.Error(), "artifact.name") {
		t.Fatalf("name mismatch err = %v", err)
	}
	if _, err := Validate(Artifact{Body: "## AC1\ncovered"}, c, "1. one 2. two"); err == nil || !strings.Contains(err.Error(), "criterion 2") {
		t.Fatalf("missing criterion err = %v", err)
	}
}
