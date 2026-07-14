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
	paths := []string{
		filepath.Join(root, ".satelle", "workflows", "satelle-project-workflow.md"),
		filepath.Join(root, ".satelle", "workflows", "satelle-parent-workflow.md"),
		filepath.Join(root, ".satelle", "workflows", "satelle-substrate-workflow.md"),
		filepath.Join(root, ".satelle", "workflows", "satelle-task-workflow.md"),
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-baseline-workflow.md"),
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-parent-workflow.md"),
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-task-workflow.md"),
	}
	tags := []string{"surface:ui", "surface:cli", "web", "feature"}
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
			if len(st.AppliesTo) > 0 {
				t.Errorf("%s node %s has applies_to=%v — AC3 assumes none on shipped workflows", p, st.Name, st.AppliesTo)
			}
		}
		for status := range statuses {
			a := spec.ScopedReviewers(status, nil)
			b := spec.ScopedReviewers(status, tags)
			if len(a) != len(b) {
				t.Errorf("%s status %s: nil-tags %d != tagged %d", p, status, len(a), len(b))
				continue
			}
			for i := range a {
				if a[i].Skill != b[i].Skill {
					t.Errorf("%s status %s: skill order/set drifted: %v vs %v", p, status, a, b)
					break
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
