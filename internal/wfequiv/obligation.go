// Obligation list → wfdot.Spec: the PROTOTYPE second constructor.
//
// This is the shape the epic (sty_c754b5c8) proposes to replace authored DOT
// with: a declaration of done (an ordered obligation list) plus a step catalogue,
// from which the route is DERIVED. It lives in wfequiv, not wfdot, because this
// story ships no production format — the real constructor is sty_4fc3d7f7. What
// this file exists to answer is narrower and prior: CAN the shape express what
// the authored graph expresses? Every gap it cannot encode is RETURNED, never
// dropped, so the answer is honest.
package wfequiv

import (
	"fmt"
	"sort"

	"github.com/bobmcallan/satelle/internal/wfdot"
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
	// Skills are the executor rubrics this step performs, in order.
	Skills []string
	// Reviewers are the gates on ENTRY to this step, all-must-accept.
	Reviewers []string
	// ReviewerAgent is the agents.toml binding the entry gates run under.
	// Empty means [reviewer].
	ReviewerAgent string
	// Parallel is the entry gates' concurrency cap (0 = sequential).
	Parallel int
	// Provides is the obligation this step discharges. A step with no Provides
	// is a role state (park, cancel, terminal marker), not an obligation-bearer.
	Provides string
	// Requires are the obligations that must be discharged before this step.
	Requires []string
	// AppliesTo scopes the step to stories carrying a matching tag.
	AppliesTo []string
	// Start marks the entry state; Terminal marks a terminal success state.
	Start    bool
	Terminal bool
}

// Gate is an obligation discharged by a reviewer rather than a stage — the
// edge-less always-on gates the DOT declares with on="<state>". It gates ENTRY to
// the named steps.
type Gate struct {
	// Skill is the reviewer rubric.
	Skill string
	// Agent is the agents.toml binding; empty means [reviewer]. A coded check
	// names none (the engine returns before gate binding).
	Agent string
	// On names the steps this gate fires on entry to. "*" means every step.
	On []string
	// AppliesTo scopes the gate to stories carrying a matching tag.
	AppliesTo []string
	// Mandatory marks a gate whose failure must be surfaced, not swallowed.
	Mandatory bool
}

// List is a category's declaration of done: the steps that discharge its
// obligations, the gates that judge them, and the role states the binary is
// expected to synthesise rather than have authored.
type List struct {
	// Category is the story category this list applies to.
	Category string
	// Steps is the catalogue. Declaration order is the tie-break when the
	// prerequisite graph does not fully order two steps.
	Steps []Step
	// Gates are the edge-less always-on reviewers.
	Gates []Gate
	// Park is the name of the park state synthesised from every non-terminal
	// step. Empty means no park state.
	Park string
	// ParkGate is the reviewer that judges entry to Park.
	ParkGate string
	// Cancel is the name of the cancel sink synthesised from every non-terminal
	// step. Empty means no cancel sink.
	Cancel string
	// CancelGate is the reviewer that judges entry to Cancel.
	CancelGate string
	// Recover names the step that a failed later step returns to — the backward
	// movement the binary owns rather than the author drawing it. Empty means no
	// recovery edges.
	Recover string
	// RecoverFrom names the steps that may move back to Recover. Empty with a
	// non-empty Recover means every step after Recover.
	RecoverFrom []string
}

// Build derives a wfdot.Spec from an obligation list, returning the Spec and the
// list of things the shape COULD NOT express.
//
// The unexpressed return is the whole point of this prototype. A constructor that
// silently dropped what it could not encode would launder the epic's go/no-go
// signal into a false pass: the diff would come back clean because the checker
// was comparing a Spec against a Spec that had quietly forgotten the same things.
// Every gap is named here and surfaces in the recorded finding.
func Build(l List) (wfdot.Spec, []string) {
	var unexpressed []string
	ordered, cycle := topoSort(l.Steps)
	if cycle != "" {
		unexpressed = append(unexpressed, cycle)
	}

	spec := wfdot.Spec{}
	for _, st := range ordered {
		shape := ""
		switch {
		case st.Start:
			shape = "Mdiamond"
		case st.Terminal:
			shape = "Msquare"
		}
		skill := ""
		if len(st.Skills) > 0 {
			skill = st.Skills[0]
		}
		if len(st.Skills) > 1 {
			unexpressed = append(unexpressed, fmt.Sprintf(
				"step %q declares %d skills; a spine node carries ONE @skill rubric — the rest need edge-less augmentation nodes",
				st.Name, len(st.Skills)))
		}
		spec.States = append(spec.States, wfdot.State{
			Name:      st.Name,
			Agent:     st.Agent,
			Skill:     skill,
			Shape:     shape,
			AppliesTo: st.AppliesTo,
		})
	}

	// Gates become edge-less reviewer nodes. Their DOT node name is not part of
	// any behavioural contract (nothing keys off it), so it is derived.
	for _, g := range l.Gates {
		spec.States = append(spec.States, wfdot.State{
			Name:      "gate_" + g.Skill,
			Agent:     g.Agent,
			Skill:     g.Skill,
			On:        g.On,
			AppliesTo: g.AppliesTo,
			Mandatory: g.Mandatory,
		})
	}

	// Spine edges: consecutive obligation-bearing steps, gated on entry by the
	// TARGET step's reviewers. Gates belong to the step they admit, not to the
	// edge — that is the shape change the epic proposes.
	var spine []Step
	for _, st := range ordered {
		if st.Agent == "reviewer" && !st.Terminal {
			continue // role state, not on the spine
		}
		spine = append(spine, st)
	}
	for i := 0; i+1 < len(spine); i++ {
		from, to := spine[i], spine[i+1]
		spec.Transitions = append(spec.Transitions, wfdot.Transition{
			From:     from.Name,
			To:       to.Name,
			Skill:    firstOf(to.Reviewers),
			Skills:   to.Reviewers,
			Agent:    to.ReviewerAgent,
			Parallel: to.Parallel,
		})
	}

	// Synthesised topology — the edges the binary owns rather than the author
	// drawing them once per workflow. Cancel from every non-terminal step; park
	// from every non-terminal step; backward movement to the recovery step.
	// A role state's own gate admits entry to it, so the synthesised edges carry
	// that skill — the same propagation Parse performs for an authored
	// `agent=reviewer, prompt="@skill:…"` node. Synthesising the edge without its
	// gate would silently drop the cancel and park reviewers.
	if l.Cancel != "" {
		spec.States = append(spec.States, wfdot.State{
			Name: l.Cancel, Agent: "reviewer", Skill: l.CancelGate,
		})
		for _, st := range spine {
			if st.Terminal {
				continue
			}
			spec.Transitions = append(spec.Transitions, cancelEdge(st.Name, l))
		}
	}
	if l.Park != "" {
		spec.States = append(spec.States, wfdot.State{
			Name: l.Park, Agent: "reviewer", Skill: l.ParkGate, From: []string{"*"},
		})
		// Park-from-anywhere: the authored DOT writes from="*" and Parse expands it
		// into explicit inbound edges, so the constructor must expand it too. The
		// start state is excluded — nothing has begun there to park.
		for _, st := range spine {
			if st.Terminal || st.Start {
				continue
			}
			spec.Transitions = append(spec.Transitions, parkEdge(st.Name, l))
		}
		if l.Cancel != "" {
			spec.Transitions = append(spec.Transitions, cancelEdge(l.Park, l))
		}
	}
	if l.Recover != "" {
		for _, name := range recoverSources(l, spine) {
			spec.Transitions = append(spec.Transitions, wfdot.Transition{
				From: name, To: l.Recover,
			})
		}
	}

	unexpressed = append(unexpressed, unexpressedNotes(l)...)
	return spec, unexpressed
}

func cancelEdge(from string, l List) wfdot.Transition {
	tr := wfdot.Transition{From: from, To: l.Cancel}
	if l.CancelGate != "" {
		// Agent left empty: empty means [reviewer], the same binding an authored
		// role-state edge carries. Naming it explicitly would read as divergence.
		tr.Skill, tr.Skills = l.CancelGate, []string{l.CancelGate}
	}
	return tr
}

func parkEdge(from string, l List) wfdot.Transition {
	tr := wfdot.Transition{From: from, To: l.Park}
	if l.ParkGate != "" {
		tr.Skill, tr.Skills = l.ParkGate, []string{l.ParkGate}
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

// unexpressedNotes names the authored-DOT constructs this prototype shape has no
// field for. Kept as a function rather than inline so the list reads as one
// inventory of the gap between the two representations.
func unexpressedNotes(l List) []string {
	var out []string
	for _, st := range l.Steps {
		if st.Agent == "" && !st.Start && !st.Terminal {
			out = append(out, fmt.Sprintf("step %q has no agent and is neither start nor terminal", st.Name))
		}
	}
	return out
}

// topoSort orders steps by their prerequisite obligations — the epic's rule that
// order is derived, never chosen by an agent. Declaration order breaks ties, so
// the result is deterministic. A cycle or an unsatisfiable prerequisite returns a
// non-empty problem string and the steps in declaration order.
func topoSort(steps []Step) ([]Step, string) {
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
	// Prerequisite edges between step names.
	deps := map[string][]string{}
	for _, st := range steps {
		for _, req := range st.Requires {
			p, ok := provider[req]
			if !ok {
				return steps, fmt.Sprintf(
					"step %q requires obligation %q which no step provides", st.Name, req)
			}
			deps[st.Name] = append(deps[st.Name], p)
		}
	}
	var out []Step
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var problem string
	var visit func(name string) bool
	visit = func(name string) bool {
		switch state[name] {
		case 2:
			return true
		case 1:
			problem = fmt.Sprintf("prerequisite cycle through step %q", name)
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
			return steps, problem
		}
	}
	return out, ""
}

func firstOf(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
