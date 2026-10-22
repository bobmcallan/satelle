package structure

import "testing"

// sty_23ceeca2 AC3/AC5: a skill's evidence declaration and the route source's
// skill_evidence value are structure, so a malformed one fails validation now
// rather than a dispatch later.
func TestSkillEvidenceDeclaration(t *testing.T) {
	skill := func(fm string) string {
		return "---\nname: my-skill\ntype: skill\ndescription: does a thing\n" + fm + "---\n\n# My skill\n\nDo the thing carefully."
	}
	if p := Doc("skills", "my-skill", skill("evidence_sections: Premise check, Existing owner\nevidence_commands: go test\n"), nil); len(p) != 0 {
		t.Errorf("a well-formed evidence declaration should pass, got %v", p)
	}
	for name, fm := range map[string]string{
		"unknown key": "evidence_command: go test\n",
		"empty value": "evidence_tools:\n",
		"empty item":  "evidence_keys: body,,name\n",
	} {
		if p := Doc("skills", "my-skill", skill(fm), nil); !hasProb(p, "evidence declaration") {
			t.Errorf("%s: want an evidence declaration problem, got %v", name, p)
		}
	}
	if SkillDeclaresEvidence(skill("")) || !SkillDeclaresEvidence(skill("evidence_tools: Read\n")) || SkillDeclaresEvidence(skill("evidence_tools:\n")) {
		t.Error("SkillDeclaresEvidence must be true only for a well-formed, non-empty declaration")
	}
}

func TestRouteSourceSkillEvidenceValue(t *testing.T) {
	all := func(string) bool { return true }
	done := func(mode string) string {
		d := routeHalf("done", "[\"*\"]\nobligations = [\"raised\"]\n")
		if mode == "" {
			return d
		}
		return d[:len("[meta]\n")] + "skill_evidence = \"" + mode + "\"\n" + d[len("[meta]\n"):]
	}
	for _, ok := range []string{"", "record", "reject"} {
		if p := Doc("workflows", "done", done(ok), all); hasProb(p, "skill_evidence") {
			t.Errorf("skill_evidence %q should be accepted, got %v", ok, p)
		}
	}
	if p := Doc("workflows", "done", done("sometimes"), all); !hasProb(p, `skill_evidence "sometimes"`) {
		t.Errorf("an unrecognised skill_evidence value must be rejected, got %v", p)
	}
}
