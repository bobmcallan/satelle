package agentartifact

import (
	"strings"
	"testing"
	"time"
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

func TestParseAttemptPolicy(t *testing.T) {
	p, err := ParseAttemptPolicy(`---
attempt_repair_max: 2
attempt_escalate_max: 1
attempt_max_total: 4
attempt_token_budget: 9000
attempt_time_budget: 3m
attempt_on_exhaust: fail
attempt_initial_effort: low
attempt_repair_effort: medium
attempt_escalate_effort: high
attempt_escalate_binding: stronger
---
rubric`)
	if err != nil {
		t.Fatal(err)
	}
	if p.RepairMax != 2 || p.EscalateMax != 1 || p.MaxTotal != 4 ||
		p.TokenBudget != 9000 || p.TimeBudget != 3*time.Minute ||
		p.InitialEffort != "low" || p.RepairEffort != "medium" ||
		p.EscalateEffort != "high" || p.EscalateBinding != "stronger" {
		t.Fatalf("policy = %#v", p)
	}
	if !p.Active() {
		t.Fatal("declared policy should be active")
	}
}

func TestParseAttemptPolicyRejectsInvalidOrBypassingValues(t *testing.T) {
	for _, body := range []string{
		"---\nattempt_repair_max: -1\n---\n",
		"---\nattempt_time_budget: forever\n---\n",
		"---\nattempt_on_exhaust: attach\n---\n",
	} {
		if _, err := ParseAttemptPolicy(body); err == nil {
			t.Fatalf("expected invalid policy for %q", body)
		}
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

func TestValidateAllReportsEveryMissingCriterion(t *testing.T) {
	_, findings := ValidateAll(Artifact{Body: "## AC1\ncovered"}, Contract{
		Name: "plan", Type: "plan", Required: true, ACCoverage: true,
	}, "1. first\n2. second\n3. third")
	got := strings.Join(findings, "\n")
	for _, want := range []string{"criterion 2", "criterion 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("findings %q missing %q", got, want)
		}
	}
}
