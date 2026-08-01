//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged (sty_c6d093c8 AC3):
// none of the four active workflows carries step-level applies_to today, so
// ScopedReviewers(status, nil) equals ScopedReviewers(status, anyTags) for every
// declared status. Unknown attrs must not appear either (AC9 audit).
func TestScopedReviewerAppliesTo_ShippedWorkflowsUnchanged(t *testing.T) {
	root := repoRootForTest()
	// This repo's own lifecycle is a DERIVED route, checked per category below;
	// the embedded defaults are still authored graphs (sty_9835070d).
	paths := []string{
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-baseline-workflow.md"),
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-parent-workflow.md"),
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-task-workflow.md"),
	}
	tags := []string{"surface:ui", "surface:cli", "web", "feature"}
	specs := map[string]wfdot.Spec{}
	for _, category := range []string{"*", "epic-parent", "parent", "substrate", "execution", "task"} {
		specs["derived route ("+category+")"] = repoRouteSpec(t, category, nil)
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
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			// parent may live only under project or only under embedded
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", p, err)
		}
		spec, ok := wfdot.Parse(string(body))
		if !ok {
			t.Fatalf("parse %s", p)
		}
		if probs := wfdot.Validate(spec); len(probs) > 0 {
			t.Errorf("%s Validate: %v", p, probs)
		}
		// Collect statuses from states + a few fixed targets.
		statuses := map[string]bool{"in_progress": true, "done": true, "release": true, "plan": true}
		for _, st := range spec.States {
			statuses[st.Name] = true
			// After sty_e4359efe the project workflow may declare design with
			// applies_to=surface:ui; other workflows should still be unscoped.
			if len(st.AppliesTo) > 0 && st.Name != "design" {
				t.Errorf("%s node %s has unexpected applies_to=%v", p, st.Name, st.AppliesTo)
			}
		}
		for status := range statuses {
			a := spec.ScopedReviewers(status, nil)
			// Compare unscoped-only sets: surface-scoped nodes (e.g. design) may
			// appear only when tags match, which is intentional (sty_e4359efe).
			b := spec.ScopedReviewers(status, tags)
			aSet, bSet := map[string]bool{}, map[string]bool{}
			for _, s := range a {
				aSet[s.Skill] = true
			}
			for _, s := range b {
				bSet[s.Skill] = true
			}
			// Every skill in a (nil-tags) must still be in b (tagged) — absent
			// applies_to is always enqueued.
			for sk := range aSet {
				if !bSet[sk] {
					t.Errorf("%s status %s: skill %s present without tags but missing with tags", p, status, sk)
				}
			}
		}
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
