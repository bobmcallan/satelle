// Package wfroute renders a story's ROUTE: the ordered steps between where the
// story is and done, each with the obligation it discharges, who performs it,
// under which rubrics, and which reviewers gate entry to it (sty_39e2d9df).
//
// It exists because the authored graph is going away. An operator used to read
// `.satelle/workflows/<name>.md` to find out why a gate fired; under a DERIVED
// route there is no graph to read, so the binary owes that back as an artifact on
// the story. This package is the rendering half — a leaf with no store, no ctx and
// no verb import, so it can be tested against a Spec literal.
//
// The input is a wfdot.Spec, which is deliberately blind to its front door:
// wfdot.Parse (authored DOT) and wfdot.ParseRoute (done.md + step.md) both
// produce one, so the same route renders either way. Nothing here knows THIS
// repo's step names — every name, gate and obligation comes from the Spec.
//
// Legibility budget: one line per step. If a route cannot render in roughly ten
// lines it is too dynamic, and that is the signal the route is meant to give.
package wfroute

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Reviewer is one gate admitting entry to a step. Scoped marks an always-on gate
// the workflow declares edge-lessly (on="<state>") rather than one named on the
// edge; ByTag names the applies_to that admitted a surface-scoped gate, so a
// reviewer present only because the story carries a tag says so.
type Reviewer struct {
	Skill  string   `json:"skill"`
	Agent  string   `json:"agent,omitempty"`
	Scoped bool     `json:"scoped,omitempty"`
	ByTag  []string `json:"by_tag,omitempty"`
}

// Step is one stop on the route.
type Step struct {
	// Status is the status the story holds while in this step.
	Status string `json:"status"`
	// Obligation is what the step discharges. Derived routes declare it; an
	// authored DOT does not carry one, and the field is then empty.
	Obligation string `json:"obligation,omitempty"`
	// Agent allocates the performer: executor (in-loop), a named isolated agent,
	// or empty for a state nothing performs.
	Agent string `json:"agent,omitempty"`
	// Skills are the executor rubrics performed in this step, tag-filtered.
	Skills []string `json:"skills,omitempty"`
	// Reviewers gate ENTRY to this step, in run order (edge-named first, then the
	// always-on scoped layer).
	Reviewers []Reviewer `json:"reviewers,omitempty"`
	// Skipped are surface-scoped gates that WOULD have run had the story carried
	// their tag. Recorded so "no gate" is distinguishable from "gate not for you".
	Skipped []Reviewer `json:"skipped,omitempty"`
	// Terminal marks the route's success end.
	Terminal bool `json:"terminal,omitempty"`
}

// Exit is an off-route destination — a park or cancel state the story may leave
// to from the spine. Not a step: nothing on the route passes through it.
//
// Park distinguishes an exit the story can still move on from (a park state,
// resumed to its recorded origin) from one it cannot (a terminal sink such as
// cancelled). The test is the graph's own: outgoing edges or none.
type Exit struct {
	Status string   `json:"status"`
	Gates  []string `json:"gates,omitempty"`
	Park   bool     `json:"park,omitempty"`
}

// Route is the whole artifact's PLAN half: the ordered spine plus the exits.
type Route struct {
	Workflow string `json:"workflow,omitempty"`
	Steps    []Step `json:"steps"`
	Exits    []Exit `json:"exits,omitempty"`
}

// Build derives the route a story with these tags will walk under spec.
//
// The spine is every state that can still reach a terminal success state
// (shape=Msquare), ordered by DISTANCE to it — furthest first. That is the same
// notion of "forward" AdvanceOptions uses, applied to the whole path rather than
// one hop, and it holds for a derived Spec without change because BuildRoute
// marks its terminal step the same way. States that reach no success terminal
// (park, cancel) are exits, not steps.
func Build(spec wfdot.Spec, workflow string, tags []string) Route {
	r := Route{Workflow: workflow}
	dist := distToSuccess(spec)

	order := map[string]int{}
	for i, st := range spec.States {
		order[st.Name] = i
	}
	var spine []wfdot.State
	for _, st := range spec.States {
		if _, ok := dist[st.Name]; !ok {
			continue
		}
		if len(st.On) > 0 {
			continue // edge-less augmentation / scoped gate, not a stop on the route
		}
		spine = append(spine, st)
	}
	sort.SliceStable(spine, func(i, j int) bool {
		di, dj := dist[spine[i].Name], dist[spine[j].Name]
		if di != dj {
			return di > dj
		}
		return order[spine[i].Name] < order[spine[j].Name]
	})

	onSpine := map[string]bool{}
	for _, st := range spine {
		onSpine[st.Name] = true
	}
	for _, st := range spine {
		r.Steps = append(r.Steps, buildStep(spec, st, tags))
	}
	r.Exits = buildExits(spec, onSpine)
	return r
}

// buildStep assembles one step's obligation, performer and entry gates.
func buildStep(spec wfdot.Spec, st wfdot.State, tags []string) Step {
	step := Step{
		Status:     st.Name,
		Obligation: st.Obligation,
		Agent:      st.Agent,
		Skills:     spec.ExecutorSkillsFor(st.Name, tags),
		Terminal:   st.Shape == "Msquare",
	}
	// Edge-named gates: the reviewers on any inbound edge. A step is entered from
	// one place on the spine and from recovery edges, which repeat the same gate
	// list — de-duplicate by skill so the route shows the gate once.
	seen := map[string]bool{}
	for _, tr := range spec.Transitions {
		if tr.To != st.Name {
			continue
		}
		for _, sk := range tr.Skills {
			if sk == "" || seen[sk] {
				continue
			}
			seen[sk] = true
			step.Reviewers = append(step.Reviewers, Reviewer{Skill: sk, Agent: tr.Agent})
		}
	}
	// The always-on scoped layer, exactly as the engine enqueues it.
	enqueued, skipped := spec.ScopedReviewersSplit(st.Name, tags)
	for _, sr := range enqueued {
		if seen[sr.Skill] {
			continue
		}
		seen[sr.Skill] = true
		step.Reviewers = append(step.Reviewers, Reviewer{
			Skill: sr.Skill, Agent: sr.Agent, Scoped: true, ByTag: appliesTo(spec, sr.Skill),
		})
	}
	for _, sr := range skipped {
		step.Skipped = append(step.Skipped, Reviewer{
			Skill: sr.Skill, Agent: sr.Agent, Scoped: true, ByTag: appliesTo(spec, sr.Skill),
		})
	}
	return step
}

// buildExits lists the off-spine destinations reachable from the spine, with the
// gate that admits entry to each.
func buildExits(spec wfdot.Spec, onSpine map[string]bool) []Exit {
	hasOut := map[string]bool{}
	for _, tr := range spec.Transitions {
		hasOut[tr.From] = true
	}
	seen := map[string]bool{}
	var out []Exit
	for _, tr := range spec.Transitions {
		if !onSpine[tr.From] || onSpine[tr.To] || seen[tr.To] {
			continue
		}
		seen[tr.To] = true
		out = append(out, Exit{Status: tr.To, Gates: tr.Skills, Park: hasOut[tr.To]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status < out[j].Status })
	return out
}

// appliesTo returns the applies_to a scoped gate carries, so a tag-scoped gate
// can say WHICH tag put it on the route. Empty means it is on by default.
func appliesTo(spec wfdot.Spec, skill string) []string {
	for _, st := range spec.States {
		if st.Skill == skill && len(st.On) > 0 {
			return st.AppliesTo
		}
	}
	return nil
}

// distToSuccess returns each state's shortest distance to a terminal SUCCESS
// state (shape=Msquare), walking edges in reverse. States absent from the map
// cannot reach one and are therefore off-route.
func distToSuccess(spec wfdot.Spec) map[string]int {
	rev := map[string][]string{}
	for _, tr := range spec.Transitions {
		rev[tr.To] = append(rev[tr.To], tr.From)
	}
	dist := map[string]int{}
	var queue []string
	for _, st := range spec.States {
		if st.Shape != "Msquare" {
			continue
		}
		if _, ok := dist[st.Name]; ok {
			continue
		}
		dist[st.Name] = 0
		queue = append(queue, st.Name)
	}
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		for _, from := range rev[n] {
			if _, ok := dist[from]; ok {
				continue
			}
			dist[from] = dist[n] + 1
			queue = append(queue, from)
		}
	}
	return dist
}

// Render writes the route as markdown — one line per step, so the whole plan for
// a six-step workflow fits in the legibility budget. `at` is the story's current
// status; the step it names is marked, which is what makes this readable without
// opening any workflow file.
func (r Route) Render(at string) string {
	var b strings.Builder
	b.WriteString("## Route\n\n")
	if r.Workflow != "" {
		fmt.Fprintf(&b, "Derived from workflow `%s`. Order is the workflow's, not the agent's.\n\n", r.Workflow)
	}
	if len(r.Steps) == 0 {
		b.WriteString("(no route — the governing workflow declares no path to a terminal success state)\n")
		return b.String()
	}
	for i, s := range r.Steps {
		marker := " "
		if s.Status == at {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %d. **%s**%s%s%s\n", marker, i+1, s.Status,
			renderObligation(s), renderPerformer(s), renderGates(s))
	}
	if len(r.Exits) > 0 {
		var exits []string
		for _, e := range r.Exits {
			label := e.Status
			if e.Park {
				label += " (park — resumes to origin)"
			} else {
				label += " (terminal)"
			}
			if len(e.Gates) > 0 {
				label += " via " + strings.Join(e.Gates, "+")
			}
			exits = append(exits, label)
		}
		fmt.Fprintf(&b, "\nExits (off-route): %s\n", strings.Join(exits, "; "))
	}
	return b.String()
}

func renderObligation(s Step) string {
	if s.Obligation == "" {
		return ""
	}
	return " — " + s.Obligation
}

// renderPerformer names WHO performs the step and under which rubrics. A state
// with neither — a start marker, a terminal — is not performed at all and says
// nothing, rather than claiming an in-loop performer that never runs.
func renderPerformer(s Step) string {
	if len(s.Skills) == 0 {
		if s.Terminal {
			return " · terminal"
		}
		if s.Agent == "" {
			return ""
		}
		return " · " + s.Agent
	}
	who := s.Agent
	if who == "" {
		who = "in-loop"
	}
	return fmt.Sprintf(" · %s runs @skill:%s", who, strings.Join(s.Skills, ", @skill:"))
}

func renderGates(s Step) string {
	if len(s.Reviewers) == 0 && len(s.Skipped) == 0 {
		return " · entry ungated"
	}
	var parts []string
	for _, rv := range s.Reviewers {
		label := rv.Skill
		if len(rv.ByTag) > 0 {
			label += " (by tag " + strings.Join(rv.ByTag, "|") + ")"
		}
		parts = append(parts, label)
	}
	for _, rv := range s.Skipped {
		parts = append(parts, rv.Skill+" (skipped: needs tag "+strings.Join(rv.ByTag, "|")+")")
	}
	return " · entry gated by " + strings.Join(parts, ", ")
}
