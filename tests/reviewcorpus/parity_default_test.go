package reviewcorpus

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sty_23e10d92 AC8: the EMBEDDED default enables bundling on an edge only when
// the corpus can show the bundle keeps every rubric on that edge honest — at
// least one known defect and one known-valid case for each rubric the bundle
// carries. Where the corpus cannot, the edge stays opt-in, and repos that want
// the saving turn it on themselves with `bundle = true`.
//
// This is a guard, not a decision: the decision is the author's, from a live
// parity run (TestParityLive in internal/agentstep). What it pins is that the
// default cannot claim parity the corpus never measured.

func embeddedBody(t *testing.T, kind, name string) string {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			return d.Body
		}
	}
	t.Fatalf("no embedded %s/%s", kind, name)
	return ""
}

// embeddedLLMGates lists an edge's gates that would ride a bundle: those whose
// embedded rubric carries no check and does not mark itself independent.
func embeddedLLMGates(t *testing.T, skills []string) []string {
	t.Helper()
	var out []string
	for _, s := range skills {
		body := ""
		for _, d := range config.EmbeddedDefaults() {
			if d.Kind == "skills" && d.Name == s {
				body = d.Body
			}
		}
		if body == "" { // not shipped: a repo-authored rubric — it would still ride the bundle
			out = append(out, s)
			continue
		}
		if structure.CheckCommand(body) != "" || structure.Independent(body) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestEmbeddedDefaultBundlesOnlyWhereTheCorpusCoversEveryRubric(t *testing.T) {
	cases, err := Load(ParityCorpusRoot())
	if err != nil {
		t.Fatal(err)
	}
	covered := func(skill string) (defect, valid bool) {
		for _, c := range cases {
			if c.Skill != skill {
				continue
			}
			defect = defect || c.Label == LabelDefect
			valid = valid || c.Label == LabelValid
		}
		return defect, valid
	}
	done, step := embeddedBody(t, "workflows", "done"), embeddedBody(t, "workflows", "step")
	for _, category := range []string{"*", "docs", "epic-parent", "parent", "execution", "task"} {
		spec, err := wfdot.ParseRoute(done, step, category, nil)
		if err != nil {
			t.Fatalf("derive embedded route for %q: %v", category, err)
		}
		for _, tr := range spec.Transitions {
			llm := embeddedLLMGates(t, tr.Skills)
			if len(llm) < 2 {
				continue
			}
			var missing []string
			for _, s := range llm {
				if d, v := covered(s); !d || !v {
					missing = append(missing, s)
				}
			}
			if !tr.Bundle {
				if len(missing) > 0 {
					t.Logf("%s %s→%s stays opt-in: no corpus defect and valid case for %s",
						category, tr.From, tr.To, strings.Join(missing, ", "))
				}
				continue
			}
			if len(missing) > 0 {
				t.Errorf("embedded default bundles %s %s→%s but the corpus has no known defect and known-valid case for %s — "+
					"run TestParityLive, and only enable bundling where every rubric on the edge is covered and the bundle holds parity",
					category, tr.From, tr.To, strings.Join(missing, ", "))
			}
		}
	}
}

// TestEmbeddedClosedEdgeStaysOptIn records the outcome today: the shipped
// working lane's `closed` step carries workflow-change-review and
// story-scope-review, which have no corpus case, so it is not bundled by default.
func TestEmbeddedClosedEdgeStaysOptIn(t *testing.T) {
	spec, err := wfdot.ParseRoute(embeddedBody(t, "workflows", "done"), embeddedBody(t, "workflows", "step"), "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tr := range spec.Transitions {
		if tr.To != "done" || len(tr.Skills) < 3 {
			continue
		}
		found = true
		if tr.Bundle {
			t.Errorf("the closed edge %s→%s must stay opt-in: the corpus has no defect case for its scope and workflow-change rubrics", tr.From, tr.To)
		}
	}
	if !found {
		t.Fatal("the embedded working lane no longer has a multi-reviewer done edge — update this guard")
	}
}
