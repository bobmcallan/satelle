package wfgovern_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The binary ships its default lifecycle as a derived route (sty_3795e7f6), and
// the doc index overlays an embedded default wherever the repo has no file of
// that name. So the two halves surface in EVERY repo's workflow set, including
// one that authored a DOT graph and never converted. Without a precedence rule
// the shipped route would shadow that graph the moment the binary upgraded —
// these tests are the rule.

const routeDone = `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "fixture declaration of done."

["*"]
obligations = ["raised", "coded", "closed"]
cancel = { state = "cancelled", gate = "cancel-review" }
`

const routeStep = `[meta]
name = "step"
type = "workflow"
scope = "system"
description = "fixture step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["intent-review"]
requires = ["raised"]

[closed]
status = "done"
reviewers = ["done-review"]
terminal = true
requires = ["coded"]
`

// authoredGraph is a repo's own workflow file that is NOT a route source — the
// unconverted case. It claims the wildcard, so it outranks the shipped route.
const authoredGraph = `---
name: my-workflow
type: workflow
scope: project
applies_to: ["*"]
description: A repo-authored wildcard lifecycle that was never converted.
---

# my workflow
`

func routeDocs(embedded bool) []docindex.Doc {
	return []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: routeDone, Embedded: embedded},
		{Kind: "workflows", Name: "step", Body: routeStep, Embedded: embedded},
	}
}

func graphDoc() docindex.Doc {
	return docindex.Doc{Kind: "workflows", Name: "my-workflow", Body: authoredGraph}
}

// TestShippedRouteYieldsToAnAuthoredWorkflow: the shipped route is ORDER ZERO —
// it does not govern a category a repo's own workflow claims.
//
// With the DOT front end retired (sty_d953c5d8) that workflow carries no
// lifecycle satelle can read, so the honest answer is a REFUSAL naming the
// remedy — not a silent fallback to the shipped route (which would re-route the
// repo behind its back) and not ErrNoWorkflow (which callers treat as a fresh
// repo and let the transition through ungated).
func TestShippedRouteYieldsToAnAuthoredWorkflow(t *testing.T) {
	docs := append(routeDocs(true), graphDoc())
	if _, ok := wfgovern.RouteGoverns(docs, "feature"); ok {
		t.Error("the shipped route must not govern a category an authored workflow claims")
	}
	_, name, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if err == nil {
		t.Fatal("a workflow that declares no route must refuse, not resolve")
	}
	if errors.Is(err, wfgovern.ErrNoWorkflow) {
		t.Error("this is not the ungoverned case — a workflow claims the category and cannot be read")
	}
	// The remedy must be something the agent can ACT on: the conversion guide
	// that maps its graph onto the two files (sty_d953c5d8).
	if !strings.Contains(err.Error(), "satelle help workflow-convert") {
		t.Errorf("the refusal must name the conversion guide, got %v", err)
	}
	if name != "my-workflow" {
		t.Errorf("the refusal must name the workflow at fault, got %q", name)
	}
}

// TestShippedRouteGovernsWhenNothingIsAuthored: with no authored workflow, the
// shipped route is what governs — this is the fresh-repo case, and the reason
// the engine needs no by-name fallback of its own.
func TestAuthoredBrokenRouteIsNotUngoverned(t *testing.T) {
	brokenDone := strings.Replace(routeDone,
		`cancel = { state = "cancelled", gate = "cancel-review" }`,
		`park = { state = "blocked", agent = "reviewer", gate = "blocked-review" }`,
		1)
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: brokenDone, Path: "/repo/.satelle/workflows/done.toml"},
		{Kind: "workflows", Name: "step", Body: routeStep, Path: "/repo/.satelle/workflows/step.toml"},
	}
	_, _, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if err == nil {
		t.Fatal("a broken authored route must refuse")
	}
	if !errors.Is(err, wfgovern.ErrRouteSourceBroken) {
		t.Errorf("err = %v, want ErrRouteSourceBroken", err)
	}
	if errors.Is(err, wfgovern.ErrNoWorkflow) {
		t.Error("must not collapse into ErrNoWorkflow")
	}
	if !strings.Contains(err.Error(), "satelle reindex") {
		t.Errorf("broken-route refusal must name satelle reindex, got %v", err)
	}
}

func TestErrNoWorkflowNamesReindex(t *testing.T) {
	_, _, _, err := wfgovern.SpecFor(nil, workitem.Item{ID: "sty_1", Category: "feature"})
	if !errors.Is(err, wfgovern.ErrNoWorkflow) {
		t.Fatalf("err = %v, want ErrNoWorkflow", err)
	}
	if !strings.Contains(err.Error(), "satelle reindex") {
		t.Errorf("ErrNoWorkflow must name satelle reindex, got %v", err)
	}
}

func TestEmbeddedBrokenRouteDoesNotBrick(t *testing.T) {
	// A parse failure on an EMBEDDED half is a broken binary; we must not
	// refuse every repo. Embedded skip matches LegacyMarkdownRoute.
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: "not toml", Embedded: true, Path: "embedded:workflows/done.toml"},
		{Kind: "workflows", Name: "step", Body: routeStep, Embedded: true, Path: "embedded:workflows/step.toml"},
	}
	_, _, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if errors.Is(err, wfgovern.ErrRouteSourceBroken) {
		t.Fatalf("embedded parse failure must not be ErrRouteSourceBroken: %v", err)
	}
}

func TestShippedRouteGovernsWhenNothingIsAuthored(t *testing.T) {
	docs := routeDocs(true)
	if _, ok := wfgovern.RouteGoverns(docs, "feature"); !ok {
		t.Fatal("the shipped route must govern when no authored workflow claims the category")
	}
	spec, name, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if err != nil {
		t.Fatalf("SpecFor: %v", err)
	}
	if name != wfgovern.DerivedRouteName {
		t.Errorf("governing lifecycle = %q, want %q", name, wfgovern.DerivedRouteName)
	}
	if len(spec.Transitions) == 0 {
		t.Error("the shipped route derived no transitions — a lifecycle with no edges gates nothing")
	}
}

// TestAuthoredRouteBeatsAnAuthoredWorkflow: a repo that CONVERTED is governed by
// what it wrote, graph or no graph. Only the shipped route yields.
func TestAuthoredRouteBeatsAnAuthoredWorkflow(t *testing.T) {
	docs := append(routeDocs(false), graphDoc())
	if _, ok := wfgovern.RouteGoverns(docs, "feature"); !ok {
		t.Fatal("an authored route must govern even beside an authored workflow")
	}
	_, name, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if err != nil {
		t.Fatalf("SpecFor: %v", err)
	}
	if name != wfgovern.DerivedRouteName {
		t.Errorf("governing lifecycle = %q, want the authored route", name)
	}
}

// TestMixedPlaneRouteCountsAsAuthored: a repo that authored ONE half is taken to
// intend a route — the shipped half completes it rather than the pair being
// discarded, and a mismatch surfaces as a build error, never as a silent
// fallback to a graph.
func TestMixedPlaneRouteCountsAsAuthored(t *testing.T) {
	docs := []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: routeDone, Embedded: false},
		{Kind: "workflows", Name: "step", Body: routeStep, Embedded: true},
		graphDoc(),
	}
	rs, ok := wfgovern.RouteGoverns(docs, "feature")
	if !ok {
		t.Fatal("a half-authored route must still govern — the repo intends a route")
	}
	if rs.Embedded {
		t.Error("a route with an authored half must not read as shipped")
	}
}

// TestNoLifecycleAtAllIsAnError: neither a route nor a workflow means nothing
// governs, and that must be an error rather than an empty Spec — a lifecycle
// resolving to no gates would let a story advance ungated.
func TestNoLifecycleAtAllIsAnError(t *testing.T) {
	_, _, _, err := wfgovern.SpecFor(nil, workitem.Item{ID: "sty_1", Category: "feature"})
	if !errors.Is(err, wfgovern.ErrNoWorkflow) {
		t.Errorf("err = %v, want ErrNoWorkflow", err)
	}
}

// TestShippedRouteStillGovernsAnUnclaimedCategory: an authored workflow claiming
// ONE category does not switch the whole repo off the shipped route — only that
// category. A category-specific graph beside the default lane is a normal repo.
func TestShippedRouteStillGovernsAnUnclaimedCategory(t *testing.T) {
	specific := docindex.Doc{Kind: "workflows", Name: "web-workflow",
		Body: "---\nname: web-workflow\ntype: workflow\nscope: project\napplies_to: [\"web\"]\ndescription: x\n---\n"}
	docs := append(routeDocs(true), specific)
	if _, ok := wfgovern.RouteGoverns(docs, "web"); ok {
		t.Error("category web is claimed by an authored workflow — the shipped route must yield there")
	}
	if _, ok := wfgovern.RouteGoverns(docs, "feature"); !ok {
		t.Error("category feature is unclaimed — the shipped route must still govern it")
	}
}
