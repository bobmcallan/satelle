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
// Precedence is RouteGoverns, and it is now the WHOLE rule: a derived route
// claiming the item's category (or the wildcard) governs, except that the route
// the BINARY ships yields to an authored workflow for the categories that
// workflow claims. There is no second representation to fall back to — the DOT
// front end is retired (sty_d953c5d8), so a repo whose workflows dir holds only
// graphs resolves to nothing and gets ErrNoWorkflow naming the remedy.
func SpecFor(workflows []docindex.Doc, item workitem.Item) (wfdot.Spec, string, []wfroute.Advisor, error) {
	category := WorkflowCategory(item)
	rs, ok := RouteGoverns(workflows, category)
	if !ok {
		if wf, governs := GoverningWorkflow(LifecycleWorkflows(workflows), item); governs {
			// A workflow doc claims the category but carries no lifecycle satelle can
			// read. Saying so beats ErrNoWorkflow: the caller treats "nothing governs"
			// as a fresh repo and lets the transition through, which for a repo that
			// believes it IS governed would silently drop every gate it authored.
			return wfdot.Spec{}, wf.Name, nil, fmt.Errorf(
				"wfgovern: workflow %q declares no route — a lifecycle is done.md + step.md under .satelle/workflows. "+
					"Read `satelle help workflow-convert` for how to convert this graph, then `satelle migrate --yes` to retire it",
				wf.Name)
		}
		return wfdot.Spec{}, "", nil, fmt.Errorf("%w: category %q", ErrNoWorkflow, category)
	}
	spec, advisors, err := routeSpec(rs, category, item.Tags)
	if err != nil {
		return wfdot.Spec{}, DerivedRouteName, nil, err
	}
	return spec, DerivedRouteName, advisors, nil
}

// routeSpec derives the Spec and the advisors from the two authored bodies.
// Split from SpecFor so the advisors come off the same parse the Spec did,
// rather than the caller re-parsing to find them.
func routeSpec(rs RouteSource, category string, tags []string) (wfdot.Spec, []wfroute.Advisor, error) {
	d, err := RouteSpecFor(rs, category, tags)
	return d.Spec, d.Advisors, err
}

// DerivedRoute is one category's resolved route: the Spec every consumer reads,
// plus the two authored halves it came from. The List and Catalogue ride along
// because a RENDERER needs to say what did NOT make it — which done.md section
// actually governed (the wildcard silently changes the answer), which gates a
// `for:` excluded, and which topology the binary synthesised rather than the
// author drawing it. None of that is recoverable from the Spec alone.
type DerivedRoute struct {
	Spec      wfdot.Spec
	List      wfdot.List
	Catalogue wfdot.Catalogue
	Advisors  []wfroute.Advisor
}

// RouteSpecFor derives one category's route from the two authored halves. It is
// the SINGLE derivation chain: routeSpec delegates here, so a renderer and the
// engine cannot drift into answering the same question differently (sty_a989764d).
func RouteSpecFor(rs RouteSource, category string, tags []string) (DerivedRoute, error) {
	lists, err := wfdot.ParseDone(rs.Done)
	if err != nil {
		return DerivedRoute{}, fmt.Errorf("wfgovern: done.md: %w", err)
	}
	cat, err := wfdot.ParseSteps(rs.Step)
	if err != nil {
		return DerivedRoute{}, fmt.Errorf("wfgovern: step.md: %w", err)
	}
	l, err := wfdot.ListFor(lists, category)
	if err != nil {
		return DerivedRoute{}, fmt.Errorf("wfgovern: %w", err)
	}
	spec, err := wfdot.BuildRoute(l, cat, tags)
	if err != nil {
		return DerivedRoute{}, fmt.Errorf("wfgovern: route for category %q: %w", category, err)
	}
	return DerivedRoute{Spec: spec, List: l, Catalogue: cat, Advisors: wfroute.AdvisorsFrom(l, cat)}, nil
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
