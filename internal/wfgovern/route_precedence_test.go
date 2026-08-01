package wfgovern_test

import (
	"errors"
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

const routeDone = `---
name: done
type: workflow
scope: system
description: fixture declaration of done.
---

## *
- raised
- coded
- closed
cancel: cancelled @cancel-review
`

const routeStep = `---
name: step
type: workflow
scope: system
description: fixture step catalogue.
---

## backlog
start: true
provides: raised

## in_progress
agent: executor
reviewers: intent-review
provides: coded
requires: raised

## done
reviewers: done-review
terminal: true
provides: closed
requires: coded
`

const authoredGraph = `---
name: my-workflow
type: workflow
scope: project
applies_to: ["*"]
description: A repo-authored wildcard lifecycle.
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress
  in_progress -> done [agent=reviewer, prompt="@skill:my-done-review"]
}
` + "```" + `
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

// TestShippedRouteYieldsToAnAuthoredWorkflow: the embedded route is ORDER ZERO.
// A repo that authored a graph claiming the category keeps being governed by it.
func TestShippedRouteYieldsToAnAuthoredWorkflow(t *testing.T) {
	docs := append(routeDocs(true), graphDoc())
	if _, ok := wfgovern.RouteGoverns(docs, "feature"); ok {
		t.Error("the shipped route must not govern a category an authored workflow claims")
	}
	_, name, _, err := wfgovern.SpecFor(docs, workitem.Item{ID: "sty_1", Category: "feature"})
	if err != nil {
		t.Fatalf("SpecFor: %v", err)
	}
	if name != "my-workflow" {
		t.Errorf("governing lifecycle = %q, want the authored workflow", name)
	}
}

// TestShippedRouteGovernsWhenNothingIsAuthored: with no authored workflow, the
// shipped route is what governs — this is the fresh-repo case, and the reason
// the engine needs no by-name fallback of its own.
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
