package web

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// A lifecycle is a DERIVED ROUTE — done.md + step.md (sty_d953c5d8). The route
// rendering is covered against that grammar below (TestWorkflowRouteFromRoute
// and friends); the retired DOT sample and the test that read it are gone with
// the front end.

const sampleDone = `[feature]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked", gate = "satelle-story-blocked-review", advisor = "blocked-triage", advisor_skill = "satelle-story-blocked-triage" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
`

const sampleStep = `[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["satelle-story-intent-review"]
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
skills = ["code"]
reviewers = ["satelle-story-plan-review"]
requires = ["planned"]

[closed]
status = "done"
terminal = true
reviewers = ["satelle-story-release-review"]
requires = ["coded"]
advise = { agent = "retrospective", skill = "satelle-lessons" }
`

// TestWorkflowRouteFromDoneStep: with a declaration of done and a step catalogue
// in the substrate, the panel derives the route from THEM — obligations appear
// (a DOT has no obligation vocabulary) and declared advisors ride along.
func TestWorkflowRouteFromDoneStep(t *testing.T) {
	set := []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Kind: "workflows", Name: wfgovern.RouteSourceStep, Body: sampleStep},
	}
	if !wfgovern.RouteSourceOf(set).Present() {
		t.Fatal("RouteSourceOf did not pick up done + step")
	}
	r := workflowRoute(set, set[0], "feature", nil)
	var got []string
	for _, s := range r.Steps {
		got = append(got, s.Status+"="+s.Obligation)
	}
	want := "backlog=raised,plan=planned,in_progress=coded,done=closed"
	if strings.Join(got, ",") != want {
		t.Fatalf("derived route = %v, want %s", got, want)
	}
	var advised int
	for _, s := range r.Steps {
		if s.Advisor != nil {
			advised++
			if s.Status == "done" && s.Advisor.Agent != "retrospective" {
				t.Errorf("done advisor = %q, want retrospective", s.Advisor.Agent)
			}
		}
	}
	if advised == 0 {
		t.Error("a derived route should carry the advisors its step catalogue declares")
	}
	var parked bool
	for _, e := range r.Exits {
		if e.Status == "blocked" {
			parked = true
			if !e.Park {
				t.Error("blocked should be a PARK exit (resumes to origin)")
			}
			if e.Advisor == nil || e.Advisor.Agent != "blocked-triage" {
				t.Errorf("blocked exit advisor = %+v, want blocked-triage", e.Advisor)
			}
		}
	}
	if !parked {
		t.Errorf("exits = %+v, want a blocked park exit", r.Exits)
	}
	// An unknown category yields no route rather than a silently ungated one.
	if len(workflowRoute(set, set[0], "nonesuch", nil).Steps) != 0 {
		t.Error("an unknown category with no wildcard section must not resolve to a route")
	}
	// …but a `*` section governs it, which is how a wildcard workflow converts.
	wild := []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone, Body: strings.Replace(sampleDone, "[feature]", `["*"]`, 1)},
		{Kind: "workflows", Name: wfgovern.RouteSourceStep, Body: sampleStep},
	}
	if len(workflowRoute(wild, wild[0], "nonesuch", nil).Steps) != 4 {
		t.Error("a `## *` section must govern a category with no section of its own")
	}
}

// TestWorkflowDetailRendersRouteNotDiagram (AC1): the expand shows the ordered
// route — step, obligation, performer, rubrics, entry gates — and NO diagram.
func TestWorkflowDetailRendersRouteNotDiagram(t *testing.T) {
	set := []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Kind: "workflows", Name: wfgovern.RouteSourceStep, Body: sampleStep},
	}
	vm := workflowDetailVM{
		Name:      "satelle-project-workflow",
		AppliesTo: []string{"feature"},
		Route:     workflowRoute(set, set[0], "feature", nil),
		Body:      "definition",
	}
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "workflowDetail", vm); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		"in_progress", "coded", "executor", "@skill:code",
		"satelle-story-plan-review", "entry gated by",
		"blocked", "park — resumes to origin",
		"advisor:", "retrospective", "nothing dispatches it",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("workflowDetail missing %q:\n%s", want, html)
		}
	}
	// The ordinal marks the route's order without a diagram.
	if !strings.Contains(html, `class="route"`) {
		t.Errorf("workflowDetail missing the route list:\n%s", html)
	}
	for _, gone := range []string{"<svg", "wf-diagram", "wf-edge-path", "wf-toggle-alt", "<h4>Flow</h4>"} {
		if strings.Contains(html, gone) {
			t.Errorf("workflowDetail still renders the retired diagram (%q):\n%s", gone, html)
		}
	}
}

// TestWorkflowDetailEmptyRoute: a workflow with no path to a terminal success
// state says so, rather than rendering a blank panel.
func TestWorkflowDetailEmptyRoute(t *testing.T) {
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, "workflowDetail", workflowDetailVM{Name: "w"}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if !strings.Contains(buf.String(), "no route") {
		t.Errorf("empty route should render an explicit empty state:\n%s", buf.String())
	}
}

// TestWorkflowRowsListRouteAsOneRow: done.md and step.md are two halves of ONE
// route, not two workflows — they get no row of their own, and the route they
// build gets exactly one, at the head. A panel that listed neither would show a
// converted repo nothing at all (sty_d953c5d8).
func TestWorkflowRowsListRouteAsOneRow(t *testing.T) {
	rows := workflowRows([]docindex.Doc{
		{Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Name: wfgovern.RouteSourceStep, Body: sampleStep},
		{Name: "satelle-project-workflow", Body: "---\nname: satelle-project-workflow\n---\n# not a route\n"},
	}, nil, nil)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the route plus the workflow", rows)
	}
	if rows[0].Name != wfgovern.DerivedRouteName {
		t.Errorf("the route must head the list, got %q", rows[0].Name)
	}
	// The row must expand through a doc the fragment handler can resolve — the
	// displayed name is two filenames, not one doc.
	if rows[0].ExpandName != wfgovern.RouteSourceDone {
		t.Errorf("route row expands %q, want %q", rows[0].ExpandName, wfgovern.RouteSourceDone)
	}
	if rows[1].Name != "satelle-project-workflow" || rows[1].ExpandName != "satelle-project-workflow" {
		t.Errorf("an authored workflow expands through its own name, got %+v", rows[1])
	}
}

// TestWorkflowRowsWithoutRoute: half a route is not a route, so an unconverted
// repo gets no phantom row.
func TestWorkflowRowsWithoutRoute(t *testing.T) {
	rows := workflowRows([]docindex.Doc{
		{Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Name: "satelle-project-workflow", Body: "---\nname: satelle-project-workflow\n---\n# not a route\n"},
	}, nil, nil)
	if len(rows) != 1 || rows[0].Name != "satelle-project-workflow" {
		t.Fatalf("rows = %+v, want only the workflow", rows)
	}
}

// TestWebHoldsNoWorkflowParser (AC3): the second DOT parser and its diagram
// renderer are gone, and nothing may reintroduce them. internal/wfdot is the one
// workflow front door; internal/web renders what it returns.
func TestWebHoldsNoWorkflowParser(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	banned := []string{"parseWorkflowDOT", "parseWorkflow", "parseState", "workflowDiagram", "edgeGateLabel", "wfdot.ParseRoute"}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, b := range banned {
			if strings.Contains(string(src), b) {
				t.Errorf("%s reintroduces %s — the web layer must hold no workflow parser of its own", name, b)
			}
		}
	}
}
