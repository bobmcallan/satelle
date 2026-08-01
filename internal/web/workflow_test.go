package web

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

const sampleWorkflowDOT = `---
name: satelle-project-workflow
applies_to: ["*"]
---
# Project workflow (DOT)

` + "```dot" + `
digraph satelle_workflow {
  graph [goal="Drive a story to done", vars="story, repo_root"]
  rankdir=LR
  backlog     [shape=Mdiamond]
  done        [shape=Msquare]

  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=executor, prompt="@skill:code"]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  design      [agent=reviewer, prompt="@skill:satelle-design-review", on="in_progress", applies_to="surface:ui"]

  backlog -> plan [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  plan -> in_progress [agent=reviewer, prompt="@skill:satelle-story-plan-review,satelle-story-architecture-review", parallel=true]
  in_progress -> done [agent=reviewer, prompt="@skill:satelle-story-release-review"]
  backlog -> cancelled
  in_progress -> cancelled
}
` + "```" + `
`

func TestFrontmatterListWeb(t *testing.T) {
	got := frontmatterList(sampleWorkflowDOT, "applies_to")
	if len(got) != 1 || got[0] != "*" {
		t.Fatalf("applies_to = %v, want [*]", got)
	}
}

// TestWorkflowRouteFromDOT: an authored DOT resolves to the ordered route — the
// spine in workflow order, each step's performer and rubrics, and the reviewers
// that gate ENTRY to it. Park/cancel destinations are exits, not steps.
func TestWorkflowRouteFromDOT(t *testing.T) {
	dotDoc := docindex.Doc{Kind: "workflows", Name: "w", Body: sampleWorkflowDOT}
	r := workflowRoute([]docindex.Doc{dotDoc}, dotDoc, "", nil)
	var got []string
	for _, s := range r.Steps {
		got = append(got, s.Status)
	}
	want := []string{"backlog", "plan", "in_progress", "done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("route steps = %v, want %v", got, want)
	}
	byStatus := map[string]int{}
	for i, s := range r.Steps {
		byStatus[s.Status] = i
	}
	if s := r.Steps[byStatus["plan"]]; s.Agent != "planner" || len(s.Skills) != 1 || s.Skills[0] != "plan" {
		t.Errorf("plan step performer = %q %v, want planner [plan]", s.Agent, s.Skills)
	}
	if s := r.Steps[byStatus["plan"]]; len(s.Reviewers) != 1 || s.Reviewers[0].Skill != "satelle-story-intent-review" {
		t.Errorf("plan entry gates = %+v, want satelle-story-intent-review", s.Reviewers)
	}
	if s := r.Steps[byStatus["in_progress"]]; len(s.Reviewers) != 2 {
		t.Errorf("in_progress entry gates = %+v, want the two plan-edge reviewers", s.Reviewers)
	}
	if !r.Steps[byStatus["done"]].Terminal {
		t.Error("done must be the route's terminal step")
	}
	// The scoped design gate applies only to surface:ui — without the tag it is
	// SKIPPED, not absent, so "no gate" stays distinguishable from "not for you".
	ip := r.Steps[byStatus["in_progress"]]
	if len(ip.Skipped) != 1 || ip.Skipped[0].Skill != "satelle-design-review" {
		t.Errorf("in_progress skipped = %+v, want satelle-design-review", ip.Skipped)
	}
	tagged := workflowRoute([]docindex.Doc{dotDoc}, dotDoc, "", []string{"surface:ui"})
	var found bool
	for _, s := range tagged.Steps {
		for _, rv := range s.Reviewers {
			if rv.Skill == "satelle-design-review" {
				found = true
			}
		}
	}
	if !found {
		t.Error("a surface:ui story should carry the scoped design gate on its route")
	}
	// cancelled is an exit, never a step.
	for _, s := range r.Steps {
		if s.Status == "cancelled" {
			t.Error("cancelled must be an exit, not a step")
		}
	}
	if len(r.Exits) != 1 || r.Exits[0].Status != "cancelled" {
		t.Errorf("exits = %+v, want cancelled", r.Exits)
	}
}

const sampleDone = `# Definition of done

## feature
- raised
- planned
- coded
- closed
park: blocked @satelle-story-blocked-review advise blocked-triage @satelle-story-blocked-triage
cancel: cancelled @satelle-story-cancel-review
`

const sampleStep = `# Step catalogue

## backlog
start: true
provides: raised

## plan
agent: planner
skills: plan
reviewers: satelle-story-intent-review
provides: planned
requires: raised

## in_progress
agent: executor
skills: code
reviewers: satelle-story-plan-review
provides: coded
requires: planned

## done
terminal: true
reviewers: satelle-story-release-review
provides: closed
requires: coded
advise: retrospective @satelle-lessons
`

// TestWorkflowRouteFromDoneStep: with a declaration of done and a step catalogue
// in the substrate, the panel derives the route from THEM — obligations appear
// (a DOT has no obligation vocabulary) and declared advisors ride along.
func TestWorkflowRouteFromDoneStep(t *testing.T) {
	set := []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Kind: "workflows", Name: wfgovern.RouteSourceStep, Body: sampleStep},
		{Kind: "workflows", Name: "w", Body: sampleWorkflowDOT},
	}
	if !wfgovern.RouteSourceOf(set).Present() {
		t.Fatal("RouteSourceOf did not pick up done + step")
	}
	// The DOT body is present and must LOSE: a derived route wins when it exists.
	r := workflowRoute(set, set[2], "feature", nil)
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
	if len(workflowRoute(set[:2], set[2], "nonesuch", nil).Steps) != 0 {
		t.Error("an unknown category with no wildcard section must not resolve to a route")
	}
	// …but a `*` section governs it, which is how a wildcard workflow converts.
	wild := []docindex.Doc{
		{Kind: "workflows", Name: wfgovern.RouteSourceDone, Body: strings.Replace(sampleDone, "## feature", "## *", 1)},
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

// TestWorkflowRowsSkipRouteSource: done.md and step.md are two halves of ONE
// route, not two workflows — they get no row in the panel.
func TestWorkflowRowsSkipRouteSource(t *testing.T) {
	rows := workflowRows([]docindex.Doc{
		{Name: wfgovern.RouteSourceDone, Body: sampleDone},
		{Name: wfgovern.RouteSourceStep, Body: sampleStep},
		{Name: "satelle-project-workflow", Body: sampleWorkflowDOT},
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
