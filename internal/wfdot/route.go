// Route construction: an obligation list plus a step catalogue in, a Spec out.
//
// This is the SECOND CONSTRUCTOR onto Spec, beside Parse. Parse reads authored
// DOT text; BuildRoute reads a declaration of done (done.md) and a step catalogue
// (step.md) and DERIVES the same lifecycle. Every consumer downstream — Validate,
// the reachability predicates, ScopedReviewersSplit, ExecutorSkillsFor, the
// engine, the seat, the edit gate — sees a normal Spec and cannot tell which
// front door it came through. That is the whole point: the graph stops being
// authored without the machinery that reads it changing at all.
//
// What the AUTHOR declares: obligations, steps, gates, and each step's
// prerequisites. What the BINARY owns: order (a topological sort of the
// prerequisites, never an agent's choice) and topology (cancel from every
// non-terminal step, park from anywhere, backward movement) — the boilerplate
// every authored workflow in this repo repeats identically today.
//
// No repo-specific rule belongs in this file. Skill names, category names and
// gate bindings are authored data; this code only arranges them.
package wfdot

import (
	"fmt"
	"sort"
)

// Step is one entry in the catalogue. It is deliberately flat — stage name,
// agent, skills, reviewers — per the epic's "a step is agents, skills,
// reviewers". Order is NOT a field: it is derived from Requires/Provides, so the
// orchestrator chooses the SET and never the SEQUENCE.
type Step struct {
	// Name is the stage name — the status a story holds while in this step.
	Name string
	// Agent allocates the performer: executor (in-loop), a named isolated agent
	// (planner, coder), or reviewer for a non-performing role state.
	Agent string
	// Skills are the executor rubrics this step performs. A spine step carries
	// one; declaring more is a construction error naming the step.
	Skills []string
	// Reviewers are the gates on ENTRY to this step, all-must-accept.
	Reviewers []string
	// ReviewerAgent is the agents.toml binding the entry gates run under.
	// Empty means [reviewer].
	ReviewerAgent string
	// Parallel is the entry gates' concurrency cap. Unset with two or more
	// reviewers defaults to DefaultParallelCap: concurrency is the shape's rule,
	// not an authored opt-in. An authored value always wins.
	Parallel int
	// ParallelSet distinguishes an authored parallel: 0 from an absent one.
	ParallelSet bool
	// Provides is the obligation this step discharges. A step with no Provides
	// is a role state (start or terminal marker), not an obligation-bearer.
	Provides string
	// Requires are the obligations that must be discharged before this step.
	Requires []string
	// AppliesTo scopes the step to stories carrying a matching tag.
	AppliesTo []string
	// Advisor is a named agent the ORCHESTRATOR may consult on this step, and the
	// rubric it consults under. It is a declaration, never a dispatch: under flat
	// dispatch the orchestrator is the sole scheduler, so an advisor advises the
	// orchestrator and is never fired by entry to a state (sty_05a5e203). It is
	// deliberately absent from the emitted Spec — Spec is topology; who to consult
	// is an instruction to the orchestrator, carried on the route.
	Advisor string
	// AdvisorSkill is the rubric Advisor is consulted under.
	AdvisorSkill string
	// Start marks the entry state; Terminal marks a terminal success state.
	Start    bool
	Terminal bool
}

// RouteGate is an always-on gate: a reviewer that judges entry to named steps
// rather than occupying a stage of its own. It is the declarative form of the
// DOT's edge-less on="<state>" node.
type RouteGate struct {
	// Skill is the reviewer rubric.
	Skill string
	// Agent is the agents.toml binding; empty means [reviewer]. A coded check
	// names none — the engine returns before gate binding.
	Agent string
	// On names the steps this gate fires on entry to. "*" means every step.
	On []string
	// AppliesTo scopes the gate to stories carrying a matching tag.
	AppliesTo []string
	// For scopes the gate to named declaration-of-done SECTIONS — the categories
	// whose route it belongs to. Empty means every route in the catalogue.
	//
	// The catalogue is shared, so without this a deployment gate on `done` would
	// fire on every category's terminal step, including the routes that
	// deliberately have no release to verify. AppliesTo cannot express it: that
	// axis is the story's TAGS, this one is which route the gate is part of
	// (sty_9835070d). A section is named by its category, so the wildcard section
	// is named `*` here like anywhere else.
	For []string
	// Mandatory marks a gate whose failure must be surfaced, not swallowed.
	Mandatory bool
}

// List is one category's declaration of done: the obligations that must be
// discharged, in order, plus the role states the binary synthesises rather than
// having them authored once per workflow.
type List struct {
	// Category is the story category this list applies to.
	Category string
	// Obligations are the things that must be true before the work is done.
	Obligations []string
	// TagObligations append to Obligations when the story carries the tag.
	TagObligations map[string][]string
	// Park is the park state synthesised from every non-terminal step.
	Park string
	// ParkGate is the reviewer that judges entry to Park.
	ParkGate string
	// ParkAdvisor / ParkAdvisorSkill name the advisor the orchestrator consults
	// when it parks a story. Declared, never dispatched (see Step.Advisor).
	ParkAdvisor      string
	ParkAdvisorSkill string
	// Cancel is the cancel sink synthesised from every non-terminal step.
	Cancel string
	// CancelGate is the reviewer that judges entry to Cancel.
	CancelGate string
	// Recover names the step a later step returns to — the backward movement the
	// binary owns rather than the author drawing it.
	Recover string
	// RecoverFrom names the steps that may move back to Recover. Empty with a
	// non-empty Recover means every spine step after Recover.
	RecoverFrom []string
}

// Catalogue is the step catalogue: every step and gate available to any category.
// A category's route selects from it by obligation.
type Catalogue struct {
	Steps []Step
	Gates []RouteGate
}

// ObligationsFor returns the category's obligations plus any appended by the
// story's tags, in declaration order with tag-appended obligations last.
func (l List) ObligationsFor(tags []string) []string {
	out := append([]string(nil), l.Obligations...)
	seen := map[string]bool{}
	for _, o := range out {
		seen[o] = true
	}
	// Sorted keys: map iteration order must not reach the route.
	var keys []string
	for k := range l.TagObligations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !tagsMatchAppliesTo([]string{k}, tags) {
			continue
		}
		for _, o := range l.TagObligations[k] {
			if !seen[o] {
				seen[o], out = true, append(out, o)
			}
		}
	}
	return out
}

// BuildRoute derives a Spec for one category from its declaration of done and
// the step catalogue, for a story carrying tags.
//
// Errors are construction failures the author must fix, and each NAMES the thing
// at fault: an obligation nothing discharges, a prerequisite nothing provides, a
// cycle. A route that cannot be built must never degrade into a partial one —
// that would silently drop gates, which is exactly the authority this
// representation exists to preserve.
func BuildRoute(l List, cat Catalogue, tags []string) (Spec, error) {
	want := l.ObligationsFor(tags)
	wanted := map[string]bool{}
	for _, o := range want {
		wanted[o] = true
	}

	provided := map[string]bool{}
	for _, st := range cat.Steps {
		if st.Provides != "" {
			provided[st.Provides] = true
		}
	}
	for _, o := range want {
		if !provided[o] {
			return Spec{}, fmt.Errorf(
				"obligation %q in category %q has no discharging step", o, l.Category)
		}
	}

	// Select: obligation-bearing steps the category asked for, plus the role
	// states (start / terminal) that carry no obligation of their own.
	var selected []Step
	for _, st := range cat.Steps {
		if st.Provides == "" || wanted[st.Provides] {
			selected = append(selected, st)
		}
	}

	ordered, err := topoSortSteps(selected)
	if err != nil {
		return Spec{}, err
	}
	return assemble(ordered, cat.Gates, l)
}

// assemble turns ordered steps, gates and the list's role-state declarations into
// a Spec. Split from BuildRoute so the selection rules and the emission rules
// stay separately readable.
func assemble(ordered []Step, gates []RouteGate, l List) (Spec, error) {
	spec := Spec{}
	for _, st := range ordered {
		shape := ""
		switch {
		case st.Start:
			shape = "Mdiamond"
		case st.Terminal:
			shape = "Msquare"
		}
		if len(st.Skills) > 1 {
			return Spec{}, fmt.Errorf(
				"step %q declares %d skills; a spine step carries one rubric — declare the rest as gates",
				st.Name, len(st.Skills))
		}
		spec.States = append(spec.States, State{
			Name:       st.Name,
			Agent:      st.Agent,
			Skill:      firstOf(st.Skills),
			Obligation: st.Provides,
			Shape:      shape,
			AppliesTo:  st.AppliesTo,
		})
	}

	// Gates become edge-less nodes. Their node name carries no behavioural
	// contract — a gate is identified by its skill — so it is derived. A gate
	// scoped to other sections is not part of THIS route and emits nothing.
	for _, g := range gates {
		if !gateInRoute(g, l.Category) {
			continue
		}
		spec.States = append(spec.States, State{
			Name:      "gate_" + g.Skill,
			Agent:     g.Agent,
			Skill:     g.Skill,
			On:        g.On,
			AppliesTo: g.AppliesTo,
			Mandatory: g.Mandatory,
		})
	}

	// Spine edges: consecutive steps, gated on entry by the TARGET step's
	// reviewers. Gates belong to the step they admit, not to the edge — that is
	// the shape change, and it is why a step definition is self-sufficient.
	var spine []Step
	for _, st := range ordered {
		if st.Agent == "reviewer" && !st.Terminal {
			continue // role state, not on the spine
		}
		spine = append(spine, st)
	}
	for i := 0; i+1 < len(spine); i++ {
		from, to := spine[i], spine[i+1]
		spec.Transitions = append(spec.Transitions, Transition{
			From:     from.Name,
			To:       to.Name,
			Skill:    firstOf(to.Reviewers),
			Skills:   to.Reviewers,
			Agent:    to.ReviewerAgent,
			Parallel: parallelFor(to),
		})
	}

	// Synthesised topology — the edges the binary owns rather than the author
	// drawing them once per workflow. A role state's own gate admits entry to it,
	// so the synthesised edges carry that skill: the same propagation Parse
	// performs for an authored agent=reviewer node. Synthesising the edge without
	// its gate would silently drop the cancel and park reviewers.
	if l.Cancel != "" {
		spec.States = append(spec.States, State{
			Name: l.Cancel, Agent: "reviewer", Skill: l.CancelGate,
		})
		for _, st := range spine {
			if st.Terminal {
				continue
			}
			spec.Transitions = append(spec.Transitions, roleEdge(st.Name, l.Cancel, l.CancelGate))
		}
	}
	if l.Park != "" {
		spec.States = append(spec.States, State{
			Name: l.Park, Agent: "reviewer", Skill: l.ParkGate, From: []string{"*"},
		})
		// Park-from-anywhere: the authored DOT writes from="*" and Parse expands
		// it into explicit inbound edges, so the constructor must expand it too.
		// The start state is excluded — nothing has begun there to park.
		for _, st := range spine {
			if st.Terminal || st.Start {
				continue
			}
			spec.Transitions = append(spec.Transitions, roleEdge(st.Name, l.Park, l.ParkGate))
		}
		if l.Cancel != "" {
			spec.Transitions = append(spec.Transitions, roleEdge(l.Park, l.Cancel, l.CancelGate))
		}
	}
	if l.Recover != "" {
		for _, name := range recoverSources(l, spine) {
			spec.Transitions = append(spec.Transitions, Transition{From: name, To: l.Recover})
		}
	}
	return spec, nil
}

// parallelFor is the concurrency cap for a step's entry gates. Two or more
// reviewers run concurrently by default: all-must-accept with no short-circuit is
// the shape's rule, so a route cannot accidentally serialise a gate set by
// forgetting to opt in. An authored value always wins, including an explicit 0.
func parallelFor(st Step) int {
	if st.ParallelSet {
		return st.Parallel
	}
	if len(st.Reviewers) > 1 {
		return DefaultParallelCap
	}
	return 0
}

// roleEdge is an edge into a role state, carrying that state's own gate. Agent is
// left empty because empty means [reviewer] — the same binding an authored
// role-state edge carries, and naming it explicitly would read as divergence.
func roleEdge(from, to, gate string) Transition {
	tr := Transition{From: from, To: to}
	if gate != "" {
		tr.Skill, tr.Skills = gate, []string{gate}
	}
	return tr
}

// recoverSources returns the steps that may move backward to l.Recover: the
// explicit RecoverFrom list, or every spine step after Recover.
func recoverSources(l List, spine []Step) []string {
	if len(l.RecoverFrom) > 0 {
		return l.RecoverFrom
	}
	var out []string
	seen := false
	for _, st := range spine {
		if st.Name == l.Recover {
			seen = true
			continue
		}
		if seen && !st.Terminal {
			out = append(out, st.Name)
		}
	}
	return out
}

// topoSortSteps orders steps by their prerequisite obligations — the rule that
// order is derived, never chosen by an agent. Declaration order breaks ties, so
// the result is deterministic.
func topoSortSteps(steps []Step) ([]Step, error) {
	provider := map[string]string{} // obligation -> step name
	for _, st := range steps {
		if st.Provides != "" {
			provider[st.Provides] = st.Name
		}
	}
	index := map[string]int{}
	for i, st := range steps {
		index[st.Name] = i
	}
	deps := map[string][]string{}
	for _, st := range steps {
		for _, req := range st.Requires {
			p, ok := provider[req]
			if !ok {
				return nil, fmt.Errorf(
					"step %q requires obligation %q which no selected step provides", st.Name, req)
			}
			deps[st.Name] = append(deps[st.Name], p)
		}
	}
	var out []Step
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var failure error
	var visit func(name string) bool
	visit = func(name string) bool {
		switch state[name] {
		case 2:
			return true
		case 1:
			failure = fmt.Errorf("prerequisite cycle through step %q", name)
			return false
		}
		state[name] = 1
		prereqs := append([]string(nil), deps[name]...)
		sort.Slice(prereqs, func(i, j int) bool { return index[prereqs[i]] < index[prereqs[j]] })
		for _, p := range prereqs {
			if !visit(p) {
				return false
			}
		}
		state[name] = 2
		out = append(out, steps[index[name]])
		return true
	}
	for _, st := range steps {
		if !visit(st.Name) {
			return nil, failure
		}
	}
	return out, nil
}

func firstOf(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// gateInRoute reports whether a gate belongs to the route being built for
// category. A gate with no `for:` belongs to every route — that is the common
// case and stays unstated. A gate that names sections belongs only to those,
// matched against the section's own name (the wildcard section is `*`).
func gateInRoute(g RouteGate, category string) bool {
	if len(g.For) == 0 {
		return true
	}
	for _, c := range g.For {
		if c == category {
			return true
		}
	}
	return false
}
