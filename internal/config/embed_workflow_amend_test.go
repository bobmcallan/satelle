package config_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfhook"
)

// embeddedDefaultBody returns one embedded default artifact's body.
func embeddedDefaultBody(t *testing.T, kind, name string) string {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			return d.Body
		}
	}
	t.Fatalf("the binary must ship %s/%s", kind, name)
	return ""
}

// TestEmbeddedDefaultDeclaresTheAmendGate (sty_5c768dd3 AC2): the shipped
// declaration of done wires the amend gate ONCE, in its [meta] frontmatter
// beside create_review. amend_review is a LIFECYCLE hook — it fires outside the
// status graph — so a per-category entry (the shape park and cancel take) would
// be the wrong topology as well as repeated.
func TestEmbeddedDefaultDeclaresTheAmendGate(t *testing.T) {
	done := embeddedDefaultBody(t, "workflows", "done")

	hook, declared := wfhook.For(done, wfhook.OpAmendReview)
	if !declared {
		t.Fatal("the shipped default declares no amend_review hook — story amend is inert in every fresh repo")
	}
	if hook.Skill != "satelle-story-amend-review" {
		t.Errorf("amend_review skill = %q, want satelle-story-amend-review", hook.Skill)
	}
	if hook.Agent != wfhook.DefaultAgent || hook.AgentDeclared {
		t.Errorf("amend_review should default to the repo's [reviewer] binding, got %+v", hook)
	}
	if !hook.Verdict {
		t.Error("amend_review must carry a gate's verdict requirements")
	}
	// Declared once, for the workflow — not per lane. Comment lines explaining the
	// hook are prose, not declarations, so only real key lines are counted.
	declarations := 0
	for _, ln := range strings.Split(done, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, wfhook.OpAmendReview) {
			declarations++
		}
	}
	if declarations != 1 {
		t.Errorf("amend_review is declared %d times in the shipped done half; a lifecycle hook is declared once", declarations)
	}
	// It sits beside the sibling lifecycle hook rather than replacing it.
	if _, ok := wfhook.For(done, wfhook.OpCreateReview); !ok {
		t.Error("create_review must still be declared")
	}
}

// TestEmbeddedAmendReviewSkillShipsWithItsJudgement (sty_5c768dd3 AC1): the
// rubric ships as a system-scope default carrying the judgement the gate exists
// to make. A gate whose skill does not resolve refuses every amendment, so the
// skill and the hook above must ship together.
func TestEmbeddedAmendReviewSkillShipsWithItsJudgement(t *testing.T) {
	body := embeddedDefaultBody(t, "skills", "satelle-story-amend-review")

	if !strings.Contains(body, "scope: system") {
		t.Error("an embedded default reviewer is scope: system")
	}
	lower := strings.ToLower(body)
	// The one question the gate asks, and both of its answers.
	for _, want := range []string{"correction", "weaken", "## accept", "## reject"} {
		if !strings.Contains(lower, want) {
			t.Errorf("amend-review rubric missing load-bearing phrase %q", want)
		}
	}
	// A weakened AC is the failure the freeze exists to prevent: the rubric must
	// say so, or the promotion has shipped an empty gate.
	if !strings.Contains(lower, "dropped, narrowed, or made vaguer") {
		t.Error("the rubric must name AC weakening as the reject case")
	}
	// It must judge, not transition: no status advice belongs in an amend verdict.
	if !strings.Contains(lower, "read-only") {
		t.Error("the rubric must state the reviewer is read-only")
	}
	// Verdict contract (the engine refuses a reviewer skill without one).
	if !strings.Contains(body, `"decision"`) || !strings.Contains(body, `"notes"`) {
		t.Error("a reviewer skill must document the {decision, notes} verdict contract")
	}
}
