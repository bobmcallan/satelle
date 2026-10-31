package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestEditPermissionDenyReasonCoderStep pins the coder-step refusal text
// (sty_a7914904): the relay is named only on a step that declares a rework
// loop; otherwise the one-shot coder dispatch is named. An executor step keeps
// permitting the driver's edit.
func TestEditPermissionDenyReasonCoderStep(t *testing.T) {
	coder := engagement{
		ItemID: "sty_x", State: "in_progress", StoryStatus: "in_progress",
		StateAgent: "coder", Engaged: true, EditCapable: false,
		EditStates: []string{"integration", "release"},
	}

	withRework := coder
	withRework.StateRework = true
	got := editPermissionDenyReason(withRework, nil, "")
	for _, want := range []string{"sty_x", `"coder"`, "story rework sty_x", "Do not edit in-loop"} {
		if !strings.Contains(got, want) {
			t.Errorf("rework step reason missing %q: %s", want, got)
		}
	}

	got = editPermissionDenyReason(coder, nil, "")
	for _, want := range []string{"one-shot coder dispatch", `"coder"`, "Do not edit in-loop"} {
		if !strings.Contains(got, want) {
			t.Errorf("non-rework step reason missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "story rework") {
		t.Errorf("non-rework step must not name `story rework`: %s", got)
	}

	exec := engagement{
		ItemID: "sty_x", State: "integration", StoryStatus: "integration",
		StateAgent: "executor", Engaged: true, EditCapable: true,
		EditStates: []string{"integration", "release"},
	}
	if !editPermitted(exec, dispatchMarker{}) {
		t.Errorf("executor step must still permit the driver's edit")
	}
}

// codedStepWFs is a route whose implement step is allocated to a dispatched
// coder, with or without a rework loop on that step.
func codedStepWFs(reworkLine string) []docindex.Doc {
	return routeWFs(
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]
`+reworkLine+`

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

// TestEngagementFillsStateReworkFromTheRoute: the engagement reads the rework
// key off the derived route, and the deny text follows it end to end.
func TestEngagementFillsStateReworkFromTheRoute(t *testing.T) {
	t.Chdir(tempRepo(t)) // the deny text consults the repo for exemptions
	story := workitem.Item{ID: "sty_work", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"}
	none := func(string) (verb.WorkingOnRow, bool) { return verb.WorkingOnRow{}, false }
	for _, tc := range []struct {
		name       string
		rework     string
		wantRework bool
	}{
		{"rework declared", `rework = { consult = "reviewer", rounds = 2 }`, true},
		{"no rework key", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wfs := codedStepWFs(tc.rework)
			live, err := evaluateEngagements([]workitem.Item{story}, wfs, nil, none)
			if err != nil || len(live) != 1 {
				t.Fatalf("evaluateEngagements: live=%+v err=%v", live, err)
			}
			if live[0].StateAgent != "coder" || live[0].StateRework != tc.wantRework {
				t.Errorf("engagement = agent %q rework %v, want coder / %v", live[0].StateAgent, live[0].StateRework, tc.wantRework)
			}
			got := editPermissionDenyReason(live[0], live, "")
			if has := strings.Contains(got, "story rework"); has != tc.wantRework {
				t.Errorf("deny text names `story rework` = %v, want %v: %s", has, tc.wantRework, got)
			}
			if !tc.wantRework && !strings.Contains(got, "one-shot coder dispatch") {
				t.Errorf("a non-rework coder step must name the one-shot dispatch: %s", got)
			}
		})
	}
}

func TestStepDeclaresRework(t *testing.T) {
	rw := []wfroute.Rework{{Step: "in_progress", Consult: "reviewer-consult", Rounds: 5}}
	if !stepDeclaresRework(rw, "in_progress") {
		t.Errorf("in_progress declares rework")
	}
	if stepDeclaresRework(rw, "plan") || stepDeclaresRework(nil, "in_progress") {
		t.Errorf("only the declared step carries rework")
	}
}
