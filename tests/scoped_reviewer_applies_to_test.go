//go:build integration

package tests

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged (sty_c6d093c8 AC3):
// none of the four active workflows carries step-level applies_to today, so
// ScopedReviewers(status, nil) equals ScopedReviewers(status, anyTags) for every
// declared status. Unknown attrs must not appear either (AC9 audit).
func TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged(t *testing.T) {
	// Both shipped lifecycles are DERIVED ROUTES now — this repo's own
	// (sty_9835070d) and the binary's default (sty_3795e7f6) — so each is checked
	// per declared category rather than per file.
	tags := []string{"surface:ui", "surface:cli", "web", "feature"}
	specs := map[string]wfdot.Spec{}
	for _, category := range []string{"*", "epic-parent", "parent", "substrate", "execution", "task"} {
		specs["this repo's route ("+category+")"] = repoRouteSpec(t, category, nil)
	}
	for _, category := range embeddedRouteCategories(t) {
		specs["the shipped route ("+category+")"] = embeddedRouteSpec(t, category, nil)
	}
	check := func(p string, spec wfdot.Spec) {
		if probs := wfdot.Validate(spec); len(probs) > 0 {
			t.Errorf("%s Validate: %v", p, probs)
		}
		statuses := map[string]bool{"in_progress": true, "done": true, "release": true, "plan": true}
		for _, st := range spec.States {
			statuses[st.Name] = true
			// After sty_e4359efe the project lifecycle may declare a surface-scoped
			// design gate; nothing else should carry applies_to.
			if len(st.AppliesTo) > 0 && st.Skill != "satelle-design-review" {
				t.Errorf("%s node %s has unexpected applies_to=%v", p, st.Name, st.AppliesTo)
			}
		}
		for status := range statuses {
			a := spec.ScopedReviewers(status, nil)
			b := spec.ScopedReviewers(status, tags)
			if len(b) < len(a) {
				t.Errorf("%s status %q: tags removed a scoped reviewer (%d → %d)", p, status, len(a), len(b))
			}
		}
	}
	for name, spec := range specs {
		check(name, spec)
	}
}

// TestScopedReviewerAppliesTo_EndToEndFilter proves a surface-scoped node is
// enqueued only for matching tags (unit-level behaviour already covered in
// wfdot; this guards the parse path against a real workflow-shaped body).
func TestScopedReviewerAppliesTo_EndToEndFilter(t *testing.T) {
	body := `---
name: demo
scope: project
type: workflow
applies_to: ["*"]
---
` + "```dot" + `
digraph demo {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  design [agent=reviewer, prompt="@skill:design-review", on="in_progress", applies_to="surface:ui"]
  estimate [agent=reviewer, prompt="@skill:est", on="in_progress"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:intent"]
  in_progress -> done
}
` + "```" + "\n"
	spec, ok := wfdot.Parse(body)
	if !ok {
		t.Fatal("parse")
	}
	if probs := wfdot.Validate(spec); len(probs) > 0 {
		t.Fatalf("validate: %v", probs)
	}
	ui := skillSet(spec.ScopedReviewers("in_progress", []string{"surface:ui"}))
	cli := skillSet(spec.ScopedReviewers("in_progress", []string{"surface:cli"}))
	if !ui["design-review"] || !ui["est"] {
		t.Errorf("ui tags: want design+est, got %v", ui)
	}
	if cli["design-review"] || !cli["est"] {
		t.Errorf("cli tags: want est only, got %v", cli)
	}
	// Unknown attr fails closed
	bad := strings.Replace(body, `on="in_progress"`, `on="in_progress", when="x"`, 1)
	bspec, ok := wfdot.Parse(bad)
	if !ok {
		t.Fatal("parse bad")
	}
	probs := wfdot.Validate(bspec)
	found := false
	for _, p := range probs {
		if strings.Contains(p, "unknown") {
			found = true
		}
	}
	if !found {
		t.Errorf("want unknown attr problem, got %v", probs)
	}
}

func skillSet(ss []wfdot.ScopedReviewer) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		m[s.Skill] = true
	}
	return m
}
