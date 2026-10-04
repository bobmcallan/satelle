package wfdot

import (
	"strings"
	"testing"
)

const panelDone = `["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked", gate = "park-review" }
`

func panelSteps(extra string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
reviewers = ["panel-review"]
` + extra + `
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

func TestPanelCopiesOntoTheSpineEdgeNotTheRoleEdge(t *testing.T) {
	spec, err := ParseRoute(panelDone, panelSteps(`
panel = ["seat-a", "seat-b"]
combine = "satelle-panel-all-accept"
`), "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	var spine, role int
	for _, tr := range spec.Transitions {
		if tr.From == "backlog" && tr.To == "in_progress" {
			spine++
			if len(tr.Panel) != 2 || tr.Panel[0] != "seat-a" || tr.Combine != "satelle-panel-all-accept" {
				t.Errorf("spine edge = %+v, want the step panel", tr)
			}
		}
		if tr.To == "blocked" && len(tr.Panel) > 0 {
			t.Errorf("role edge %s→%s must not carry panel", tr.From, tr.To)
		}
		if tr.To == "blocked" {
			role++
		}
	}
	if spine != 1 || role == 0 {
		t.Fatalf("spine=%d role=%d, want the entry edge and a park edge", spine, role)
	}
}

func TestGatePanelStaysOnThatGate(t *testing.T) {
	spec, err := ParseRoute(panelDone, panelSteps("")+`
[[gate]]
skill = "scoped-review"
on = ["in_progress"]
panel = ["seat-g"]
combine = "satelle-panel-majority"
`, "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, st := range spec.States {
		if st.Skill != "scoped-review" {
			continue
		}
		found = true
		if len(st.Panel) != 1 || st.Panel[0] != "seat-g" || st.Combine != "satelle-panel-majority" {
			t.Errorf("gate state = %+v, want its own panel", st)
		}
	}
	if !found {
		t.Fatal("scoped gate was not emitted")
	}
	enqueued, skipped := spec.ScopedReviewersSplit("in_progress", nil)
	if len(enqueued) != 1 || len(enqueued[0].Panel) != 1 || enqueued[0].Panel[0] != "seat-g" {
		t.Fatalf("enqueued = %+v, want the gate panel copied", enqueued)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", skipped)
	}
	for _, tr := range spec.Transitions {
		if tr.To == "in_progress" && len(tr.Panel) > 0 {
			t.Errorf("a gate panel must not land on the spine edge: %+v", tr)
		}
	}
}

func TestPanelParseRefusalsNameTheKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"panel with reviewer_agent", `panel = ["seat-a"]
reviewer_agent = "reviewer"`, "panel together with reviewer_agent"},
		{"combine without panel", `combine = "satelle-panel-all-accept"`, "combine"},
		{"two seats without combine", `panel = ["seat-a", "seat-b"]`, "without combine"},
		{"gate panel with agent", "", "panel together with agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := panelSteps(tc.body)
			if tc.name == "gate panel with agent" {
				body = panelSteps("") + `
[[gate]]
skill = "scoped-review"
on = ["in_progress"]
agent = "reviewer"
panel = ["seat-g"]
`
			}
			_, err := ParseSteps(body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestOneSeatPanelOmitsCombine(t *testing.T) {
	cat, err := ParseSteps(panelSteps(`panel = ["seat-a"]`))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, st := range cat.Steps {
		if st.Name == "in_progress" {
			found = true
			if len(st.Panel) != 1 || st.Combine != "" {
				t.Fatalf("step = %+v, want one seat and no combine", st)
			}
		}
	}
	if !found {
		t.Fatal("in_progress step missing")
	}
}
