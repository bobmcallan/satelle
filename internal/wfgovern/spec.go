// The workflow FRONT DOOR (sty_9835070d): one place decides how a work item's
// lifecycle is resolved, so every consumer — the engine, the verbs, the edit-gate
// hooks, the web panel — sees the same route.
//
// There are two representations and exactly one precedence rule, and RouteGoverns
// is that rule. A DERIVED route (`done.md` + `step.md` under the workflows kind)
// wins when the repo AUTHORED one; the route the BINARY ships is order zero and
// yields to an authored workflow for the categories that workflow claims; an
// authored DOT graph governs otherwise. A second implementation of that rule is
// the defect this file exists to prevent.
package wfgovern

import (
	"errors"
	"fmt"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// RouteSourceDone and RouteSourceStep are the doc names of the two authored
// bodies a derived route is built from. They are indexed under the `workflows`
// kind like any authored workflow, but they are NOT workflows: neither carries a
// lifecycle of its own, and neither may be selected by applies_to.
const (
	RouteSourceDone = "done"
	RouteSourceStep = "step"
)

// DerivedRouteName is what a derived route calls itself where a workflow name is
// reported — the route document's header, `satelle workflow show`, a refusal.
// It names the two files an operator would open, because there is no graph to.
const DerivedRouteName = "done.md+step.md"

// ErrNoWorkflow reports that nothing governs the item: no derived route claims
// its category and no authored workflow applies. It is deliberately an error
// rather than an empty Spec — a lifecycle that resolves to no gates would let a
// story advance ungated.
var ErrNoWorkflow = errors.New("wfgovern: no workflow governs this item")

// IsRouteSource reports whether a workflow-kind doc is one half of a derived
// route rather than a workflow in its own right.
func IsRouteSource(name string) bool {
	return name == RouteSourceDone || name == RouteSourceStep
}

// RouteSource carries the two authored bodies. Empty until a repo converts.
type RouteSource struct {
	Done string
	Step string
	// Embedded marks a route that comes ENTIRELY from the binary's shipped
	// defaults rather than from the repo's own substrate. It is what makes the
	// shipped route ORDER ZERO: see RouteGoverns. A mixed pair (the repo authored
	// one half, the other overlaid from the defaults) counts as authored — the
	// repo intends a route, and BuildRoute fails loudly if the halves disagree.
	Embedded bool
}

// Present reports whether both halves are there. One half alone is not a route
// — and is not silently half-applied.
func (rs RouteSource) Present() bool { return rs.Done != "" && rs.Step != "" }

// RouteSourceOf picks the declaration of done and the step catalogue out of an
// already-listed workflow doc set. It reports what is THERE; whether it governs
// a given category is RouteGoverns.
func RouteSourceOf(workflows []docindex.Doc) RouteSource {
	var rs RouteSource
	doneEmbedded, stepEmbedded := false, false
	for _, w := range workflows {
		switch w.Name {
		case RouteSourceDone:
			rs.Done, doneEmbedded = w.Body, w.Embedded
		case RouteSourceStep:
			rs.Step, stepEmbedded = w.Body, w.Embedded
		}
	}
	rs.Embedded = doneEmbedded && stepEmbedded
	return rs
}

// RouteGoverns is the ONE precedence rule for a derived route, and every surface
// that resolves, displays or stamps a lifecycle asks it rather than re-deriving
// its own answer.
//
// An AUTHORED route (the repo's own done.md + step.md) governs the categories it
// claims — a repo that converted is governed by what it wrote. The route the
// BINARY ships is order zero instead: it governs a category only when no
// authored workflow claims that category. Without that distinction, shipping the
// defaults as a route would silently shadow every repo's authored graph on the
// next binary upgrade, because the doc index overlays an embedded default
// wherever the repo has no file of that name.
//
// An empty category means the wildcard view, which a route answers with its `*`
// section.
func RouteGoverns(workflows []docindex.Doc, category string) (RouteSource, bool) {
	rs := RouteSourceOf(workflows)
	if !rs.Present() || !routeClaims(rs, category) {
		return RouteSource{}, false
	}
	if rs.Embedded && len(OrderedWorkflows(LifecycleWorkflows(workflows), category)) > 0 {
		return RouteSource{}, false // an authored workflow outranks the shipped route
	}
	return rs, true
}

// routeClaims reports whether the route declares a section for category — its
// own, or the wildcard that governs everything else.
func routeClaims(rs RouteSource, category string) bool {
	for _, c := range RouteCategories(rs.Done) {
		if c == category || c == wfdot.WildcardCategory {
			return true
		}
	}
	return false
}

// LifecycleWorkflows returns the docs that carry a lifecycle — every workflow
// except the two route-source halves. Callers that pick among workflows
// (applies_to precedence, the ambiguity check, the panel list) use this so a
// route source is never treated as a workflow.
func LifecycleWorkflows(workflows []docindex.Doc) []docindex.Doc {
	out := make([]docindex.Doc, 0, len(workflows))
	for _, w := range workflows {
		if IsRouteSource(w.Name) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// SpecFor resolves the lifecycle governing item, and the advisors its route
// declares. It returns an ERROR rather than a bare not-ok: a call site that
// turned "the route does not build" into "no workflow" would let a transition
// run ungated, which is the worst failure available. Callers that GATE must
// refuse on the error (see Refusal / RuleStructureGuard); the one caller allowed
// to degrade is a read surface rendering a page, and it degrades by handling the
// error here, not by this seam hiding it.
//
// Precedence is RouteGoverns: a derived route claiming the item's category (or
// the wildcard) wins, except that the route the BINARY ships yields to an
// authored workflow; otherwise the governing authored workflow's DOT. An
// authored DOT names no advisor — entry dispatch is retired, so the graph has no
// attribute that could carry one (sty_05a5e203).
func SpecFor(workflows []docindex.Doc, item workitem.Item) (wfdot.Spec, string, []wfroute.Advisor, error) {
	category := WorkflowCategory(item)
	if rs, ok := RouteGoverns(workflows, category); ok {
		spec, advisors, err := routeSpec(rs, category, item.Tags)
		if err != nil {
			return wfdot.Spec{}, DerivedRouteName, nil, err
		}
		return spec, DerivedRouteName, advisors, nil
	}
	wf, ok := GoverningWorkflow(LifecycleWorkflows(workflows), item)
	if !ok {
		return wfdot.Spec{}, "", nil, fmt.Errorf("%w: category %q", ErrNoWorkflow, category)
	}
	spec, ok := wfdot.Parse(wf.Body)
	if !ok {
		return wfdot.Spec{}, wf.Name, nil, fmt.Errorf("wfgovern: workflow %q has no parseable DOT lifecycle", wf.Name)
	}
	return spec, wf.Name, nil, nil
}

// routeSpec derives the Spec and the advisors from the two authored bodies.
// Split from SpecFor so the advisors come off the same parse the Spec did,
// rather than the caller re-parsing to find them.
func routeSpec(rs RouteSource, category string, tags []string) (wfdot.Spec, []wfroute.Advisor, error) {
	lists, err := wfdot.ParseDone(rs.Done)
	if err != nil {
		return wfdot.Spec{}, nil, fmt.Errorf("wfgovern: done.md: %w", err)
	}
	cat, err := wfdot.ParseSteps(rs.Step)
	if err != nil {
		return wfdot.Spec{}, nil, fmt.Errorf("wfgovern: step.md: %w", err)
	}
	l, err := wfdot.ListFor(lists, category)
	if err != nil {
		return wfdot.Spec{}, nil, fmt.Errorf("wfgovern: %w", err)
	}
	spec, err := wfdot.BuildRoute(l, cat, tags)
	if err != nil {
		return wfdot.Spec{}, nil, fmt.Errorf("wfgovern: route for category %q: %w", category, err)
	}
	return spec, wfroute.AdvisorsFrom(l, cat), nil
}

// RouteCategories returns the categories a derived route claims, in declaration
// order. Used where a repo's CLAIMED categories decide something an authored
// workflow's applies_to used to — seeding defaults, most importantly, which
// would otherwise re-seed a DOT workflow over the route that replaced it.
func RouteCategories(doneBody string) []string {
	if doneBody == "" {
		return nil
	}
	lists, err := wfdot.ParseDone(doneBody)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(lists))
	for _, l := range lists {
		out = append(out, l.Category)
	}
	return out
}
