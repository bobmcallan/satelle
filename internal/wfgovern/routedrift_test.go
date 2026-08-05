package wfgovern

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// laneSpec builds a Spec whose states are the named stages, plus one always-on
// gate node — which occupies no stage and must never count as a lane state.
func laneSpec(states ...string) wfdot.Spec {
	spec := wfdot.Spec{}
	for _, s := range states {
		spec.States = append(spec.States, wfdot.State{Name: s})
	}
	spec.States = append(spec.States, wfdot.State{Name: "gate_x", Skill: "satelle-step-summary", On: []string{"done"}})
	return spec
}

// TestDetectRouteDrift covers the enumeration: the fast path that every ordinary
// transition takes, and the two drift shapes (sty_6e4f7fd8 AC1/AC4).
func TestDetectRouteDrift(t *testing.T) {
	project := laneSpec("backlog", "plan", "in_progress", "integration", "release", "done")
	docs := laneSpec("backlog", "in_progress", "done")

	cases := []struct {
		name       string
		item       workitem.Item
		spec       wfdot.Spec
		walked     []string
		wantDrift  bool
		wantOff    string
		wantOnLane bool
	}{
		{
			name:   "fast path: walked lane is the derived lane",
			item:   workitem.Item{ID: "sty_ok", Category: "feature", Status: "integration"},
			spec:   project,
			walked: []string{"backlog", "plan", "in_progress", "integration"},
		},
		{
			name:   "fast path: never transitioned",
			item:   workitem.Item{ID: "sty_new", Category: "feature", Status: "backlog"},
			spec:   project,
			walked: nil,
		},
		{
			// The motivating case: a docs table is added while a story sits at plan.
			name:      "drift: status is off the lane derived now",
			item:      workitem.Item{ID: "sty_drift", Category: "docs", Status: "plan"},
			spec:      docs,
			walked:    []string{"backlog", "plan"},
			wantDrift: true,
			wantOff:   "plan",
		},
		{
			// History is off-lane but the story sits on a state the lane declares:
			// movement is still legal, so this is the case a gate judges.
			name:       "drift: history off-lane, current status still legal",
			item:       workitem.Item{ID: "sty_soft", Category: "docs", Status: "in_progress"},
			spec:       docs,
			walked:     []string{"backlog", "plan", "in_progress"},
			wantDrift:  true,
			wantOff:    "plan",
			wantOnLane: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, drifted := DetectRouteDrift(tc.item, tc.spec, tc.walked)
			if drifted != tc.wantDrift {
				t.Fatalf("drifted = %v, want %v (%+v)", drifted, tc.wantDrift, d)
			}
			if !tc.wantDrift {
				return
			}
			if got := strings.Join(d.OffRoute, ","); got != tc.wantOff {
				t.Errorf("off_route = %q, want %q", got, tc.wantOff)
			}
			if d.StatusOnRoute != tc.wantOnLane {
				t.Errorf("status_on_route = %v, want %v", d.StatusOnRoute, tc.wantOnLane)
			}
			if strings.Contains(strings.Join(d.Derived, ","), "gate_x") {
				t.Errorf("an always-on gate node must not count as a lane state: %v", d.Derived)
			}
		})
	}
}

// TestDriftRefusalText: the refusal names BOTH lanes, and its remedy is
// mutability-aware — category is frozen once a story leaves the entry state, so
// suggesting --category there would send the operator into a refusal
// (sty_6e4f7fd8 AC2).
func TestDriftRefusalText(t *testing.T) {
	docs := laneSpec("backlog", "in_progress", "done")

	past, ok := DetectRouteDrift(workitem.Item{ID: "sty_past", Category: "docs", Status: "plan"}, docs,
		[]string{"backlog", "plan"})
	if !ok {
		t.Fatal("expected drift")
	}
	why := DriftWhy(past)
	for _, want := range []string{"plan", "docs", "backlog → in_progress → done", "backlog → plan"} {
		if !strings.Contains(why, want) {
			t.Errorf("why must name both lanes, missing %q: %s", want, why)
		}
	}
	remedy := DriftRemedy(past, "backlog")
	if !strings.Contains(remedy, "supersedes:sty_past") || strings.Contains(remedy, "--category") {
		t.Errorf("past the entry state the remedy is re-raise, never --category: %s", remedy)
	}

	entry, ok := DetectRouteDrift(workitem.Item{ID: "sty_entry", Category: "docs", Status: "backlog"}, docs,
		[]string{"backlog", "plan"})
	if !ok {
		t.Fatal("expected drift")
	}
	if r := DriftRemedy(entry, "backlog"); !strings.Contains(r, "--category") || !strings.Contains(r, "sty_entry") {
		t.Errorf("in the entry state the remedy is reclassification: %s", r)
	}
}
