package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfroute"
)

// The DERIVED-ROUTE view of `satelle workflow show` (sty_a989764d).
//
// "config over code" is only real if an operator can SEE the process. Before
// this, `satelle workflow show <category>` failed with a docindex miss and the
// only route views were the two authored halves — so the question "what route
// does a story of category X actually get?" had no answer short of deriving it
// by hand from done.md + step.md.
//
// Everything here READS the derivation; nothing re-implements it. The Spec comes
// from wfgovern.RouteSpecFor (the single chain the engine also uses), the step
// lines come from wfroute (the same renderer the story route document uses), and
// gate scoping asks wfdot.GateInRoute — the predicate BuildRoute itself asks. A
// second opinion here would be worse than no view at all: it would look
// authoritative while disagreeing with what actually runs.

// renderWorkflowRoute writes the derived route for one category. It takes the
// route SOURCE rather than the app so it is unit-testable without a store, the
// same shape renderWorkflowShow already has.
func renderWorkflowRoute(out io.Writer, rs wfgovern.RouteSource, category string, tags []string) error {
	d, err := wfgovern.RouteSpecFor(rs, category, tags)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "ROUTE %s\n", category)
	fmt.Fprintf(out, "  derived from: done.md + step.md\n")
	// Which section governed is load-bearing: the wildcard silently answers for
	// every category with no section of its own, and a reader who assumes an
	// exact match would be reading someone else's route.
	if d.List.Category == category {
		fmt.Fprintf(out, "  section:      ## %s\n", d.List.Category)
	} else {
		fmt.Fprintf(out, "  section:      ## %s (the wildcard — %q declares no section of its own)\n",
			d.List.Category, category)
	}
	if len(tags) > 0 {
		fmt.Fprintf(out, "  tags:         %s\n", strings.Join(tags, ", "))
	}
	fmt.Fprintln(out)

	route := wfroute.Build(d.Spec, wfgovern.DerivedRouteName, tags, d.Advisors)
	renderRouteSteps(out, route, d.List)
	renderRouteGateScope(out, d)
	renderSynthesised(out, route, d.List)
	return nil
}

// renderRouteSteps prints the obligations in route order with what discharges
// each and what admits it.
func renderRouteSteps(out io.Writer, route wfroute.Route, l wfdot.List) {
	fmt.Fprintln(out, "Steps (order derived from requires:, never authored):")
	if len(route.Steps) == 0 {
		fmt.Fprintln(out, "  (none — the section declares no obligation any step discharges)")
		return
	}
	for i, st := range route.Steps {
		marks := ""
		if st.Terminal {
			marks = " [terminal]"
		}
		fmt.Fprintf(out, "  %d. %s%s\n", i+1, st.Status, marks)
		if st.Obligation != "" {
			fmt.Fprintf(out, "       discharges: %s\n", st.Obligation)
		}
		performer := st.Agent
		if performer == "" {
			performer = "(nothing performs here)"
		}
		if len(st.Skills) > 0 {
			performer += " under @skill:" + strings.Join(st.Skills, ", @skill:")
		}
		fmt.Fprintf(out, "       performer:  %s\n", performer)
		if len(st.Reviewers) > 0 {
			fmt.Fprintf(out, "       entry gates: %s\n", reviewerList(st.Reviewers))
			cap := "serial"
			if st.Parallel > 0 {
				cap = fmt.Sprintf("up to %d concurrent", st.Parallel)
			}
			fmt.Fprintf(out, "                    (%s, all must accept)\n", cap)
		}
		for _, sk := range st.Skipped {
			fmt.Fprintf(out, "       not run:    %s — needs tag %s\n", sk.Skill, strings.Join(sk.ByTag, "|"))
		}
		if st.Advisor != nil {
			fmt.Fprintf(out, "       advisor:    %s @%s (consulted by the orchestrator, never dispatched by entry)\n",
				st.Advisor.Agent, st.Advisor.Skill)
		}
	}
	fmt.Fprintln(out)
}

// reviewerList renders gates with the binding each runs under, since "which
// model judged this" is half of what a reader is asking.
func reviewerList(rs []wfroute.Reviewer) string {
	var out []string
	for _, r := range rs {
		s := r.Skill
		if r.Agent != "" {
			s += " [" + r.Agent + "]"
		}
		if r.Scoped {
			s += " (always-on)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// renderRouteGateScope names the always-on gates in scope AND the ones a `for:`
// excluded. The excluded set is the point: an excluded gate is deliberately
// absent from the Spec, so without naming it here a reader cannot tell a gate
// that does not apply from a gate nobody declared.
func renderRouteGateScope(out io.Writer, d wfgovern.DerivedRoute) {
	fmt.Fprintln(out, "Always-on gates:")
	var any bool
	for _, st := range d.Spec.States {
		if len(st.On) == 0 && st.Skill == "" {
			continue
		}
		if len(st.On) == 0 && !strings.HasPrefix(st.Name, "gate_") {
			continue
		}
		any = true
		on := strings.Join(st.On, ", ")
		if on == "" {
			on = "every step"
		}
		line := fmt.Sprintf("  %s — on: %s", st.Skill, on)
		if st.Agent != "" {
			line += fmt.Sprintf(" [%s]", st.Agent)
		}
		if len(st.AppliesTo) > 0 {
			line += fmt.Sprintf(" · only for tags %s", strings.Join(st.AppliesTo, "|"))
		}
		if st.Mandatory {
			line += " · mandatory"
		}
		fmt.Fprintln(out, line)
	}
	for _, g := range d.Catalogue.Gates {
		if wfdot.GateInRoute(g, d.List.Category) {
			continue
		}
		any = true
		fmt.Fprintf(out, "  %s — EXCLUDED: for: [%s] does not name section %q\n",
			g.Skill, strings.Join(g.For, ", "), d.List.Category)
	}
	if !any {
		fmt.Fprintln(out, "  (none)")
	}
	fmt.Fprintln(out)
}

// renderSynthesised names the topology the BINARY owns rather than the author
// drawing it, marked as such so authored and derived shape are distinguishable
// at a glance.
func renderSynthesised(out io.Writer, route wfroute.Route, l wfdot.List) {
	fmt.Fprintln(out, "Synthesised topology (owned by the binary, not authored):")
	var any bool
	for _, e := range route.Exits {
		kind := ""
		switch e.Status {
		case l.Park:
			kind = "park — reachable from every non-terminal step"
		case l.Cancel:
			kind = "cancel — reachable from every non-terminal step"
		}
		if kind == "" {
			continue
		}
		any = true
		line := fmt.Sprintf("  %s: %s", e.Status, kind)
		if len(e.Gates) > 0 {
			line += fmt.Sprintf(" · admitted by %s", strings.Join(e.Gates, ", "))
		}
		fmt.Fprintln(out, line)
	}
	if l.ParkAdvisor != "" {
		fmt.Fprintf(out, "  %s advisor: %s @%s (consulted on park)\n", l.Park, l.ParkAdvisor, l.ParkAdvisorSkill)
		any = true
	}
	if l.Recover != "" {
		from := "every spine step after " + l.Recover
		if len(l.RecoverFrom) > 0 {
			from = strings.Join(l.RecoverFrom, ", ")
		}
		fmt.Fprintf(out, "  recover: back to %s from %s\n", l.Recover, from)
		any = true
	}
	if !any {
		fmt.Fprintln(out, "  (none — this section declares no park, cancel or recover)")
	}
}
