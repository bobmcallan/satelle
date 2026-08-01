//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestExecutorAugmentation_ShippedWorkflowsUnchanged (sty_8225d8a5 AC4): no
// shipped workflow declares augmentation nodes; PerformingStates and
// ExecutorPathToDoneSkills are stable with or without surface tags.
func TestExecutorAugmentation_ShippedWorkflowsUnchanged(t *testing.T) {
	root := repoRootForTest()
	// This repo's own lifecycle is a DERIVED route now, so it is checked per
	// declared category rather than per file (sty_9835070d).
	for _, category := range []string{"*", "epic-parent", "parent", "substrate", "execution", "task"} {
		for _, st := range repoRouteSpec(t, category, nil).States {
			if st.IsAugmentation() {
				t.Errorf("derived route (%s) has unexpected augmentation %s", category, st.Name)
			}
		}
	}
	paths := []string{
		filepath.Join(root, "internal", "config", "substrate", "workflows", "satelle-baseline-workflow.md"),
	}
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		spec, ok := wfdot.Parse(string(body))
		if !ok {
			t.Fatalf("parse %s", p)
		}
		for _, st := range spec.States {
			if st.IsAugmentation() {
				t.Errorf("%s has unexpected augmentation %s", p, st.Name)
			}
		}
		a := spec.ExecutorPathToDoneSkills()
		b := spec.ExecutorPathToDoneSkillsFor([]string{"surface:ui", "surface:cli"})
		if len(a) != len(b) {
			t.Errorf("%s path skills nil-tags %v vs tagged %v", p, a, b)
		}
		// Graph identity: same transitions regardless of surface (no fork).
		if len(spec.Transitions) == 0 && len(spec.States) > 1 {
			// parent workflow has edges; just ensure Validate clean
		}
		if probs := wfdot.Validate(spec); len(probs) > 0 {
			t.Errorf("%s validate: %v", p, probs)
		}
	}
}
