package wfequiv

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// sampleSpec is a miniature workflow exercising every dimension the checker
// probes: a gated spine, an edge-less always-on gate, a surface-scoped gate, an
// executor augmentation, a park state and a terminal.
func sampleSpec() wfdot.Spec {
	return wfdot.Spec{
		States: []wfdot.State{
			{Name: "backlog", Shape: "Mdiamond"},
			{Name: "in_progress", Agent: "executor", Skill: "code"},
			{Name: "done", Shape: "Msquare"},
			{Name: "cancelled", Agent: "reviewer", Skill: "cancel-review"},
			{Name: "check", Skill: "unit-check", On: []string{"done"}},
			{Name: "design", Agent: "reviewer", Skill: "design-review",
				On: []string{"done"}, AppliesTo: []string{"surface:ui"}},
			{Name: "code_ui", Agent: "executor", Skill: "code-ui",
				On: []string{"in_progress"}, AppliesTo: []string{"surface:ui"}},
		},
		Transitions: []wfdot.Transition{
			{From: "backlog", To: "in_progress", Agent: "reviewer",
				Skill: "intent-review", Skills: []string{"intent-review"}},
			{From: "in_progress", To: "done", Agent: "reviewer",
				Skill: "ac-review", Skills: []string{"ac-review", "scope-review"}, Parallel: 4},
			{From: "backlog", To: "cancelled"},
			{From: "in_progress", To: "cancelled"},
		},
	}
}

func TestDiffIdentityIsEmpty(t *testing.T) {
	s := sampleSpec()
	if r := Diff(s, s); !r.Empty() {
		t.Fatalf("identity diff must be empty, got:\n%s", r)
	}
}

// TestDiffDimensionIsolation is the assertion that proves the checker is not a
// deep-equal in disguise: each targeted mutation must light up EXACTLY the
// dimension it belongs to. Without this, a single catch-all comparison would pass
// the same tests while telling a migration nothing about what actually moved.
func TestDiffDimensionIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*wfdot.Spec)
		wantDim string
	}{
		{
			name:    "dropped edge is path divergence",
			mutate:  func(s *wfdot.Spec) { s.Transitions = s.Transitions[:len(s.Transitions)-1] },
			wantDim: "Path",
		},
		{
			name: "stripped edge gates is gate divergence",
			mutate: func(s *wfdot.Spec) {
				for i := range s.Transitions {
					if s.Transitions[i].To == "done" {
						s.Transitions[i].Skills = []string{"ac-review"}
					}
				}
			},
			wantDim: "Gates",
		},
		{
			// Re-targeting rather than clearing on=: clearing it would also turn the
			// node into an engaging lifecycle state and light up Path, which says
			// nothing about whether the dimensions are separable.
			name: "re-targeted scoped on= is scoped divergence",
			mutate: func(s *wfdot.Spec) {
				for i := range s.States {
					if s.States[i].Name == "check" {
						s.States[i].On = []string{"in_progress"}
					}
				}
			},
			wantDim: "Scoped",
		},
		{
			name: "cleared performing skill is executor divergence",
			mutate: func(s *wfdot.Spec) {
				for i := range s.States {
					if s.States[i].Name == "code_ui" {
						s.States[i].Skill = ""
					}
				}
			},
			wantDim: "Executor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := sampleSpec()
			got := sampleSpec()
			tc.mutate(&got)
			r := Diff(want, got)
			if r.Empty() {
				t.Fatalf("mutation produced no divergence")
			}
			dims := map[string]int{
				"Path": len(r.Path), "Gates": len(r.Gates),
				"Scoped": len(r.Scoped), "Executor": len(r.Executor),
			}
			if dims[tc.wantDim] == 0 {
				t.Errorf("expected divergence in %s, got none.\n%s", tc.wantDim, r)
			}
			for dim, n := range dims {
				if dim != tc.wantDim && n != 0 {
					t.Errorf("unexpected divergence in %s (want only %s):\n%s", dim, tc.wantDim, r)
				}
			}
		})
	}
}

// TestDiffScopedNeedsTagMatrix is the reason DefaultTagSets exists. Re-scoping a
// surface gate to a tag no story carries is invisible to a tag-less comparison —
// both sides declare it and both sides skip it — but it silently disarms the gate
// for every surface:ui story. A checker that compared once would green-light that.
func TestDiffScopedNeedsTagMatrix(t *testing.T) {
	want := sampleSpec()
	got := sampleSpec()
	for i := range got.States {
		if got.States[i].Name == "design" {
			got.States[i].AppliesTo = []string{"surface:none"}
		}
	}
	if r := DiffFor(want, got, [][]string{nil}); !r.Empty() {
		t.Fatalf("untagged comparison cannot see a re-scoped surface gate; got:\n%s", r)
	}
	r := Diff(want, got)
	if len(r.Scoped) == 0 {
		t.Fatalf("tag matrix must catch the disarmed surface:ui gate; got:\n%s", r)
	}
}

func TestReportStringNamesDimensions(t *testing.T) {
	r := Report{Path: []string{"a"}, Executor: []string{"b"}}
	s := r.String()
	for _, want := range []string{"PATH (1)", "EXECUTOR (1)", "- a", "- b"} {
		if !strings.Contains(s, want) {
			t.Errorf("report missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "GATES") {
		t.Errorf("empty dimension must not be printed:\n%s", s)
	}
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}
}
