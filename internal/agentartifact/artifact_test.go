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

func TestParseContractReadsCriteriaSection(t *testing.T) {
	c, err := ParseContract("---\noutput_name: plan\noutput_type: plan\noutput_criteria_section: Acceptance criteria\n---\nrubric")
	if err != nil {
		t.Fatal(err)
	}
	if c.CriteriaSection != "Acceptance criteria" || !c.Active() {
		t.Fatalf("contract = %#v", c)
	}
}

func TestCriteriaSectionFindings(t *testing.T) {
	const heading = "Acceptance criteria"
	section := func(lines ...string) string {
		return "# Plan\n\n## Acceptance criteria\n" + strings.Join(lines, "\n") + "\n\n## AC1\n- not judged here\n"
	}
	cases := []struct {
		name   string
		body   string
		keyed  bool // false: the contract does not declare the key
		want   []string
		quotes []string
	}{
		{name: "tool line-number prefix", body: section("1. one", "30|3. three"), keyed: true, want: []string{"line 2"}, quotes: []string{"30|3. three"}},
		{name: "unnumbered bullet", body: section("1. one", "- bullet"), keyed: true, want: []string{"line 2"}, quotes: []string{"- bullet"}},
		{name: "indented sub-item", body: section("1. one", "  2. sub"), keyed: true, want: []string{"line 2"}, quotes: []string{"2. sub"}},
		{name: "single-space indent", body: section("1. one", " 2. sub"), keyed: true, want: []string{"line 2"}, quotes: []string{"2. sub"}},
		{name: "prose between criteria", body: section("1. one", "some prose", "2. two"), keyed: true, want: []string{"line 2"}, quotes: []string{"some prose"}},
		{name: "several offenders", body: section("30|1. one", "2. two", "- three"), keyed: true, want: []string{"line 1", "line 3"}, quotes: []string{"30|1. one", "- three"}},
		{name: "well-formed with blanks", body: section("1. one", "", "2) two", "3. three"), keyed: true},
		{name: "section absent", body: "# Plan\n\n## AC1\n- bullet\n", keyed: true},
		{name: "heading is case-sensitive", body: "## Acceptance Criteria\n- bullet\n", keyed: true},
		{name: "section ends at next heading", body: "## Acceptance criteria\n1. one\n## Risks\n- bullet\n", keyed: true},
		{name: "no key, malformed body", body: section("- bullet")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Contract{Name: "plan", Type: "plan"}
			if tc.keyed {
				c.CriteriaSection = heading
			}
			_, findings := ValidateAll(Artifact{Body: tc.body}, c, "")
			if len(findings) != len(tc.want) {
				t.Fatalf("findings = %q, want %d", findings, len(tc.want))
			}
			for i, f := range findings {
				if !strings.Contains(f, tc.want[i]) || !strings.Contains(f, tc.quotes[i]) {
					t.Errorf("finding %d = %q, want %q quoting %q", i, f, tc.want[i], tc.quotes[i])
				}
			}
		})
	}
}

func TestCriteriaSectionFindingTruncatesLongLines(t *testing.T) {
	long := "- " + strings.Repeat("x", 200)
	got := criteriaSectionFindings("## Acceptance criteria\n"+long+"\n", "Acceptance criteria")
	if len(got) != 1 || strings.Contains(got[0], strings.Repeat("x", 100)) || !strings.Contains(got[0], "…") {
		t.Fatalf("findings = %q", got)
	}
}
