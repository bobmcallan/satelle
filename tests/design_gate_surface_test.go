//go:build integration

package tests

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// TestDesignGateSurfaceScoped (sty_e4359efe): project workflow declares design
// with applies_to surface:ui on integration; only UI-tagged stories enqueue it.
func TestDesignGateSurfaceScoped(t *testing.T) {
	spec := repoRouteSpec(t, "*", nil)
	if probs := wfdot.Validate(spec); len(probs) > 0 {
		t.Fatalf("validate: %v", probs)
	}
	// design gate present with applies_to. A derived route names a gate node
	// after its skill (gate_<skill>) — the node name carries no contract.
	found := false
	for _, st := range spec.States {
		if st.Skill == "satelle-design-review" {
			found = true
			if st.Skill != "satelle-design-review" {
				t.Errorf("skill = %q", st.Skill)
			}
			if len(st.AppliesTo) != 1 || st.AppliesTo[0] != "surface:ui" {
				t.Errorf("applies_to = %v", st.AppliesTo)
			}
			if !containsStrSlice(st.On, "integration") {
				t.Errorf("on = %v", st.On)
			}
		}
	}
	if !found {
		t.Fatal("design node missing from project workflow DOT")
	}
	ui := skillNames(spec.ScopedReviewers("integration", []string{"surface:ui"}))
	cli := skillNames(spec.ScopedReviewers("integration", []string{"surface:cli"}))
	if !ui["satelle-design-review"] {
		t.Errorf("surface:ui must enqueue design-review: %v", ui)
	}
	if cli["satelle-design-review"] {
		t.Errorf("surface:cli must NOT enqueue design-review: %v", cli)
	}
	// Spine transitions identical regardless of tags (no fork)
	if len(spec.Transitions) < 5 {
		t.Errorf("unexpected sparse spine: %d edges", len(spec.Transitions))
	}
}

func skillNames(ss []wfdot.ScopedReviewer) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		m[s.Skill] = true
	}
	return m
}

func containsStrSlice(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
