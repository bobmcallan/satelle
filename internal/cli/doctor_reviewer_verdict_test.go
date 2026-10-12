package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ctxaudit"
	"github.com/bobmcallan/satelle/internal/health"
)

// The verdict rules every judging reviewer obeys are the embedded principle
// satelle-reviewer-verdict. The reviewer skills no longer restate them, so doctor
// finds no verbatim duplication of the passage, and a judging seat's audited
// context carries the document exactly once — where the dispatch delivers it.

func contextNamed(t *testing.T, a *contextAudit, label string) ctxaudit.Context {
	t.Helper()
	for _, c := range a.contexts {
		if c.Label == label {
			return c
		}
	}
	t.Fatalf("no %s context assembled; have %v", label, func() (ls []string) {
		for _, c := range a.contexts {
			ls = append(ls, c.Label)
		}
		return
	}())
	return ctxaudit.Context{}
}

func partsNamed(c ctxaudit.Context, name string) (n int) {
	for _, p := range c.Parts {
		if p.Name == name {
			n++
		}
	}
	return n
}

func TestDoctorJudgingSeatContextCarriesTheVerdictRulesOnce(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents()+"\n[planner]\nrole = \"agent\"\ncommand = \""+claudeSeatCmd+"\"\ntools = \"Read,Grep,Glob\"\n")
	// The shipped route performs nothing out of loop, so the fixture hands the
	// coded step to a named performer: a seat that does not judge.
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		body := d.Body
		if d.Name == "step" {
			body = strings.Replace(body, "status = \"in_progress\"\nagent = \"executor\"\nreviewers = [\"satelle-story-intent-review\"]",
				"status = \"in_progress\"\nagent = \"planner\"\nreviewers = [\"satelle-story-intent-review\"]", 1)
			if body == d.Body {
				t.Fatal("fixture: the coded step of the shipped route moved")
			}
		}
		f.write("workflows/"+d.Name+d.Ext, body)
	}
	a := assembleContexts(context.Background(), f.repo, f.dataDir)

	judging := contextNamed(t, a, "seat[reviewer]")
	if n := partsNamed(judging, config.ReviewerVerdictPrinciple); n != 1 {
		t.Errorf("a judging seat's audited context carries %s once, got %d", config.ReviewerVerdictPrinciple, n)
	}
	skills := 0
	for _, p := range judging.Parts {
		if p.Kind == "skills" {
			skills++
			for _, phrase := range []string{"quotation comparison governs", "is required on every judgment"} {
				if strings.Contains(p.Body, phrase) {
					t.Errorf("embedded reviewer skill %s restates %q", p.Name, phrase)
				}
			}
		}
	}
	if skills == 0 {
		t.Fatal("fixture: the judging seat is handed reviewer skills")
	}

	performer := contextNamed(t, a, "seat[planner]")
	if n := partsNamed(performer, config.ReviewerVerdictPrinciple); n != 0 {
		t.Errorf("a seat that does not judge is not handed %s, got %d", config.ReviewerVerdictPrinciple, n)
	}
}

// duplicatedVerdictRules is the duplicate-verbatim findings whose quote carries the
// verdict rules. The quote is the normalised shingle run (lowercased, punctuation
// stripped, cut at 240 bytes), so the passage is matched as it opens that run: the
// "Re-presented edge" heading and the first sentence under it.
func duplicatedVerdictRules(f *ctxFixture) health.Findings {
	var out health.Findings
	for _, fd := range byID(checksFor(f), health.IDContextDuplicateVerbatim) {
		for _, passage := range []string{"every judging verdict sets reviewed", "re presented edge every judging"} {
			if strings.Contains(fd.Detail, passage) {
				out = append(out, fd)
				break
			}
		}
	}
	return out
}

func TestDoctorReportsNoDuplicateOfTheVerdictRulesAcrossEmbeddedSkills(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	for _, fd := range duplicatedVerdictRules(f) {
		t.Errorf("the verdict rules are stated once, yet doctor reports them duplicated: %s", fd.Detail)
	}
}

// embeddedReviewerSkills are the skills that carried the verdict rules before they
// moved to the shared principle.
var embeddedReviewerSkills = []string{
	"satelle-story-done-review", "satelle-workflow-change-review", "satelle-story-amend-review",
	"satelle-story-plan-review", "satelle-story-intent-review", "satelle-story-create-review",
	"satelle-instruction-change-review", "satelle-story-blocked-review", "satelle-story-cancel-review",
	"satelle-story-scope-review", "satelle-task-validate-after-review",
}

// No two embedded reviewer skills share a run of words doctor flags: whatever they
// have in common (a pointer, a verdict JSON block) is not repeated verbatim.
func TestDoctorReportsNoDuplicateBetweenEmbeddedReviewerSkills(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	for _, fd := range byID(checksFor(f), health.IDContextDuplicateVerbatim) {
		var named []string
		for _, s := range embeddedReviewerSkills {
			if strings.Contains(fd.Detail, "embedded:skills/"+s+".md") {
				named = append(named, s)
			}
		}
		if len(named) >= 2 {
			t.Errorf("embedded reviewer skills %v duplicate each other: %s", named, fd.Detail)
		}
	}
}

// The control: two reviewer skills that restate the passage, as the embedded ones
// did before it moved to the principle, are reported — so the check above can fail.
func TestDoctorReportsTheVerdictRulesWhenTwoSkillsRestateThem(t *testing.T) {
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	restated := "---\nname: NAME\ntype: skill\ndescription: restates the verdict rules\ntags: [type:skill]\n---\n# Review\n\n## Re-presented edge\n\n" +
		"Every judging verdict sets `reviewed` to the exact words it rests on. The first verdict on the edge has no prior row, so it judges and sets `reviewed`; a later citation cannot start until that quotation is stored. On a re-presented edge the quotation comparison governs. Open `prior_verdicts`. Take this skill's latest row. If that row has no `reviewed` string, or `reviewed_truncated` is set, re-judge the words this verdict would rest on and set `reviewed` to those exact words. Do not cite an older row that still has a complete `reviewed` string. If that latest row has a complete `reviewed` string, compare those exact words with the words this verdict would rest on. If they are the same words, re-issue that earlier verdict: the same decision, the same `reviewed` string, and notes that cite its `attempt` and skill.\n"
	for _, name := range []string{"satelle-story-done-review", "satelle-story-amend-review"} {
		f.write("skills/"+name+".md", strings.ReplaceAll(restated, "NAME", name))
	}
	if len(duplicatedVerdictRules(f)) == 0 {
		t.Error("two skills restating the verdict rules went unreported")
	}
}

// A binding that injects every principle picks the embedded document up as a
// resident one too; the audit and the dispatch both still carry it once.
func TestDoctorJudgingSeatOnSelectorAllCarriesTheVerdictRulesOnce(t *testing.T) {
	f := newCtxFixture(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \""+claudeSeatCmd+"\"\ntools = \"Read,Grep,Glob\"\nprinciples = \"all\"\n")
	a := assembleContexts(context.Background(), f.repo, f.dataDir)
	if n := partsNamed(contextNamed(t, a, "seat[reviewer]"), config.ReviewerVerdictPrinciple); n != 1 {
		t.Errorf("selector all must not double-count %s, got %d", config.ReviewerVerdictPrinciple, n)
	}
}
