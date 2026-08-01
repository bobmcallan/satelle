// Package wfdot derives a workflow's neutral Spec from its two authored halves —
// the SINGLE route-to-spec path shared by the web panel, the reviewer gater, and
// the commit/edit hooks (so the grammar is defined once, never copied).
//
// The model is step-centric: each step carries an `agent`, its `skills` rubric,
// and the `reviewers` gating ENTRY to it — so a story's status walks the steps
// and entry to a gated step is the gated transition. ORDER and topology are
// DERIVED (BuildRoute), never authored; the DOT text front end that used to
// produce a Spec is retired (sty_d953c5d8). The package keeps its name because
// Spec and its consumers are unchanged. See the satelle-agent-model and
// satelle-route-standard principles.
package wfdot

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultDoneGate is satelle's conventional close gate. It is no longer MANDATED
// by Validate (sty_9a139c78): the done gate is whatever the route's terminal
// step declares, transparently — a route may name it, name another, or omit it
// entirely ("if the user breaks the process, so be it"). The name remains the
// convention init seeds and the docs reference.
const DefaultDoneGate = "satelle-story-done-review"

// StepSummarySkill is the conventional step-review/summary skill. A route opts
// into per-transition step summaries by declaring a `## gate` section for it
// (transparently, in step.md), optionally marked `mandatory: true`. There is no
// hidden always-on summariser — the route declares it (sty_9a139c78).
const StepSummarySkill = "satelle-step-summary"

// Validate checks a parsed workflow Spec for structural soundness, returning
// human-readable problems (empty = valid):
//   - at least one state;
//   - every transition endpoint is a declared state (no dangling edge);
//   - at least one terminal state (a state with no outgoing edge);
//   - a state named "done", if present, is terminal;
//   - unknown node/edge attributes and mis-placed applies_to (sty_c6d093c8).
//
// The done gate is NOT mandated: it is whatever the workflow declares (sty_9a139c78).
func Validate(spec Spec) []string {
	if len(spec.States) == 0 {
		return []string{"workflow has no states"}
	}
	known := map[string]bool{}
	for _, s := range spec.States {
		known[s.Name] = true
	}
	hasOut := map[string]bool{}
	var problems []string
	for _, tr := range spec.Transitions {
		if !known[tr.From] {
			problems = append(problems, fmt.Sprintf("transition from unknown state %q", tr.From))
		}
		if !known[tr.To] {
			problems = append(problems, fmt.Sprintf("transition to unknown state %q", tr.To))
		}
		hasOut[tr.From] = true
	}
	terminal := 0
	for _, s := range spec.States {
		if !hasOut[s.Name] {
			terminal++
		}
	}
	if terminal == 0 {
		problems = append(problems, "workflow has no terminal state (every state has an outgoing edge)")
	}
	if known["done"] && hasOut["done"] {
		problems = append(problems, `state "done" must be terminal (it has an outgoing edge)`)
	}
	// Parse-time attr / placement problems (unknown keys, applies_to misuse).
	// Parse returns only ok/not-ok, so it CARRIES them here (sty_c6d093c8).
	problems = append(problems, spec.AttrProblems...)
	// Nodes that appear on an edge are lifecycle states; augmentation must be edge-less.
	incident := map[string]bool{}
	for _, tr := range spec.Transitions {
		incident[tr.From], incident[tr.To] = true, true
	}
	for _, st := range spec.States {
		// Performing + on= + incident edges: fail closed (would vanish from spine).
		if st.IsPerforming() && len(st.On) > 0 && incident[st.Name] {
			problems = append(problems, fmt.Sprintf(
				"performing node %q has on= but also participates in edges — augmentation nodes must be edge-less (sty_8225d8a5)",
				st.Name))
		}
		if len(st.AppliesTo) == 0 {
			continue
		}
		// Performing spine nodes (no on=): applies_to is not a node-override.
		// Edge-less performing+on= (IsAugmentation) may carry it (sty_8225d8a5).
		if st.IsPerforming() && !st.IsAugmentation() {
			problems = append(problems, fmt.Sprintf(
				"applies_to on performing node %q is not supported without on= (use an edge-less executor augmentation node: agent=executor, on=\"<state>\", applies_to=… — sty_8225d8a5)",
				st.Name))
			continue
		}
		if st.Agent == "reviewer" && len(st.On) == 0 {
			problems = append(problems, fmt.Sprintf(
				"applies_to on node %q is ignored without on= (step-level applies_to only filters edge-less scoped reviewers)",
				st.Name))
		}
	}
	return problems
}

// Start returns the workflow's initial state — the first declared state with no
// incoming transition (the Mdiamond entry, e.g. "backlog"). Empty when every
// state has an incoming edge (no clear start). The engagement edge leaves Start.
func (s Spec) Start() string {
	hasIn := map[string]bool{}
	for _, tr := range s.Transitions {
		hasIn[tr.To] = true
	}
	for _, st := range s.States {
		if !hasIn[st.Name] {
			return st.Name
		}
	}
	return ""
}

// State is one step of the derived route. Terminal is true when no transition
// leaves it.
type State struct {
	Name     string
	Agent    string
	Terminal bool
	// Skill is the step's own rubric — what an executor step performs, or the
	// gate a reviewer step judges by (empty when the step names none). Populated
	// from the step's `skills:`.
	Skill string
	// Obligation is what this step DISCHARGES — the declaration-of-done entry that
	// put it on the route. Set by BuildRoute from the step's `provides`. Carried
	// on State (not only in the route constructor) so the route a story renders is
	// the same one the Spec was built from (sty_39e2d9df).
	Obligation string
	// Mandatory is the gate's `mandatory: true` key. For the step-summary gate it
	// means the summary is required (a failure is surfaced, not swallowed); for
	// other steps it is advisory metadata.
	Mandatory bool
	// On is an always-on gate's `on:` list — the target steps it gates. Empty for
	// an ordinary step. `*` means every transition. This is the declarative
	// replacement for the old reviewer:always tag layer: the route, not a skill
	// tag, is the sole authority for which always-on gates run.
	On []string
	// Shape is the step's start/terminal classification (Mdiamond=start,
	// Msquare=terminal). Set by BuildRoute from the step's `start:` / `terminal:`
	// keys; the marker vocabulary is retained so every Spec consumer reads one
	// spelling.
	Shape string
	// AppliesTo is the node's optional applies_to="surface:ui,…" list
	// (sty_c6d093c8 / sty_8225d8a5). For an edge-less scoped reviewer or
	// executor augmentation (on= set), the node applies only when the story
	// holds a matching tag (EqualFold ANY-match). Empty means every story
	// (equivalent to ["*"]). Spine performing nodes without on= may not carry
	// applies_to (Validate rejects). One parse path serves both reviewer and
	// executor attachments.
	AppliesTo []string
	// From is the park node's optional from="*" or from="s1,s2" list
	// (sty_f75286dc): source states that may park into this node without an
	// explicit X→park edge. "*" expands to every spine performing state.
	// Empty means inbound park edges must be drawn explicitly (legacy form).
	// Resume is NOT declared here — the engine enforces resume-to-origin.
	From []string
}

// StepSummary reports whether the workflow declares a step-summary node (a node
// whose gate skill is StepSummarySkill) and whether it is marked mandatory. The
// summariser runs only when declared — there is no hidden always-on summariser
// (sty_9a139c78).
func (s Spec) StepSummary() (declared, mandatory bool) {
	_, d, m := s.StepSummaryBinding()
	return d, m
}

// StepSummaryBinding is StepSummary plus the agents.toml section named on the
// step node (agent=<name>). Empty agent means the default [reviewer] binding.
// Named role=reviewer bindings are supported so high-frequency step recaps can
// use a cheap summariser without burning the deep judgment model.
func (s Spec) StepSummaryBinding() (agent string, declared, mandatory bool) {
	for _, st := range s.States {
		if st.Skill == StepSummarySkill {
			return st.Agent, true, st.Mandatory
		}
	}
	return "", false, false
}

// ScopedReviewer is one edge-less always-on gate: its skill and optional gate
// binding section name (agent=<name>). Agent empty means [reviewer] (sty_a476a2f8).
type ScopedReviewer struct {
	Skill string
	Agent string // agents.toml section; empty → reviewer
}

// ScopedReviewers returns the DECLARED, edge-less reviewer nodes that gate the
// transition into toStatus — a reviewer node whose `on=` list includes toStatus
// or the wildcard "*". These are the route-declared always-on gates, replacing
// the old reviewer:always skill-tag scan so the route is the sole gating
// authority. The step-summary gate is excluded: it is a post-transition summariser (run via
// Summarise), not a blocking gate. Sorted by skill for a deterministic order.
//
// tags is the story's tag set (item.Tags). A node with applies_to is enqueued only
// when tagsMatchesAppliesTo holds (sty_c6d093c8). Absent applies_to matches every
// story. Matching is in-memory EqualFold ANY-match (same semantics as the store's
// json_each exact filter for already-canonical tags; case-insensitive so we do
// not repeat the plain-== trap). category and kind are NOT consulted.
func (s Spec) ScopedReviewers(toStatus string, tags []string) []ScopedReviewer {
	out, _ := s.ScopedReviewersSplit(toStatus, tags)
	return out
}

// ScopedReviewersSplit is ScopedReviewers plus the surface-scoped candidates that
// were FILTERED OUT by applies_to (sty_dcce86d5). Skipped only includes nodes
// with a non-empty applies_to that matched on= but not tags — so a skip is
// distinguishable from "no such surface gate in the workflow".
func (s Spec) ScopedReviewersSplit(toStatus string, tags []string) (enqueued, skipped []ScopedReviewer) {
	for _, st := range s.States {
		// Scoped GATE: on= + skill. agent= names the binding (default reviewer).
		// Skip known performing tokens used for executor augmentation (on=)
		// (sty_8225d8a5 / sty_a476a2f8 D2). Named judges (agent=<custom>) stay.
		if st.Skill == "" || len(st.On) == 0 {
			continue
		}
		switch st.Agent {
		case "executor", "planner", "coder":
			continue // performer / augmentation, not a gate
		}
		if st.Skill == StepSummarySkill {
			continue
		}
		if !(containsStr(st.On, "*") || containsStr(st.On, toStatus)) {
			continue
		}
		ref := ScopedReviewer{Skill: st.Skill, Agent: st.Agent}
		if !tagsMatchAppliesTo(st.AppliesTo, tags) {
			// Only applies_to-filtered nodes are "skipped"; absent applies_to never skips.
			if len(st.AppliesTo) > 0 {
				skipped = append(skipped, ref)
			}
			continue
		}
		enqueued = append(enqueued, ref)
	}
	sort.Slice(enqueued, func(i, j int) bool { return enqueued[i].Skill < enqueued[j].Skill })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Skill < skipped[j].Skill })
	return enqueued, skipped
}

// tagsMatchAppliesTo is the in-memory ANY-match for step-level applies_to
// (sty_c6d093c8). Reuses the SEMANTICS of workitem/store.json_each equality
// (not the SQL) so wfdot stays free of a DB dependency:
//   - empty appliesTo  → true (absent ≡ ["*"])
//   - any entry is "*" → true
//   - any entry EqualFold-matches a story tag → true
//
// Plain filter, no override, no tie-break. Case-insensitive so applies_to
// "Surface:UI" still matches tag surface:ui (CanonicaliseTags already stores
// declared casing for controlled namespaces). sty_8225d8a5 should call this
// same helper for executor augmentation rather than inventing a second matcher.
func tagsMatchAppliesTo(appliesTo, tags []string) bool {
	if len(appliesTo) == 0 {
		return true
	}
	for _, a := range appliesTo {
		if a == "*" {
			return true
		}
		for _, t := range tags {
			if strings.EqualFold(a, t) {
				return true
			}
		}
	}
	return false
}

// doneReachable returns the set of states from which "done" is reachable
// (inclusive of "done"), by reverse traversal. Empty when there is no "done".
func (s Spec) doneReachable() map[string]bool {
	// Seed on the conventional "done" name only when that state exists —
	// empty map when absent (executor-path short-circuit). Distance walk is
	// shared with AdvanceOptions via reverseDistFrom (sty_e16a2cd7).
	hasDone := false
	for _, st := range s.States {
		if st.Name == "done" {
			hasDone = true
			break
		}
	}
	if !hasDone {
		return map[string]bool{}
	}
	dist := s.reverseDistFrom([]string{"done"})
	reach := make(map[string]bool, len(dist))
	for n := range dist {
		reach[n] = true
	}
	return reach
}

// IsPerforming reports whether a node PERFORMS work — any node carrying a
// non-reviewer agent (the in-loop `executor` OR a named isolated agent a node
// allocates a step to, e.g. planner/coder/commit-push). A reviewer node judges,
// it does not perform; an agent-less node (a terminal marker) performs nothing.
// This is the dispatch lock-guard's predicate (internal/agentstep uses
// IsPerformingState to verify a named agent's FROM state is genuinely engaged) —
// NOT the edit-gate's engaged check, which has its own independent shape-derived
// predicate (NonTerminalEngagingStates, sty_f3d5d4b8). The two are deliberately
// separate: PerformingStates answers "which nodes dispatch work";
// NonTerminalEngagingStates answers "which statuses count as in-flight for the
// edit gate" (non-start, non-terminal).
//
// An augmentation node (IsAugmentation) is IsPerforming for skill-resolution
// purposes but is NOT a lifecycle status — see PerformingStates / isEngaging.
//
// A step-summary declaration (IsSummariser) is never performing, whatever
// agent= binding it allocates — it is a post-transition narrator, not a status
// a story can hold (sty_8ee40f94).
//
// A scoped gate (on= + skill) with a non-performer agent token is a named
// judge (e.g. agent=reviewer-summary), not a performer — without this, from="*"
// park expansion would invent phantom edges from estimate/intcheck
// (sty_8ee40f94). Known performer tokens match agentvalidate's scoped-gate skip
// list: executor, planner, coder.
func (st State) IsPerforming() bool {
	if st.IsSummariser() {
		return false
	}
	if st.Agent == "" || st.Agent == "reviewer" {
		return false
	}
	// Scoped on= + skill: only known performer tokens perform (augmentation).
	// Any other agent= on a scoped gate is a named judge.
	if len(st.On) > 0 && st.Skill != "" {
		switch st.Agent {
		case "executor", "planner", "coder":
			return true
		default:
			return false
		}
	}
	return true
}

// IsSummariser reports whether this is the edge-less step-summary declaration —
// a post-transition narrator, never a lifecycle status, whatever binding its
// agent= allocates. Keyed on skill (wfdot.StepSummarySkill), not a binding name,
// so repo-authored cheap summarisers stay repo-agnostic (sty_8ee40f94).
func (st State) IsSummariser() bool {
	return st.Skill == StepSummarySkill
}

// IsAugmentation reports whether this is an edge-less executor augmentation node
// (sty_8225d8a5): agent is a performer and on= names the spine state(s) it
// augments. Same shape as a scoped reviewer (on= attach) but agent=executor.
// Reuses on= rather than inventing augments= — attach-to-state, keyed by agent=.
// Named judges with on= (agent=reviewer-summary, …) are not augmentations —
// IsPerforming already excludes them.
func (st State) IsAugmentation() bool {
	return st.IsPerforming() && len(st.On) > 0
}

// PerformingStates returns the names of every SPINE performing node (see
// IsPerforming), in declaration order — excluding augmentation annotations
// (sty_8225d8a5), which are not statuses a story can hold.
func (s Spec) PerformingStates() []string {
	var out []string
	for _, st := range s.States {
		if st.IsPerforming() && !st.IsAugmentation() {
			out = append(out, st.Name)
		}
	}
	return out
}

// EditCapableStates returns spine performing states allocated to the in-loop
// executor. This is deliberately distinct from PerformingStates (which includes
// isolated named agents) and NonTerminalEngagingStates (which controls lease
// ownership): only the workflow's driving executor may authorize source edits
// from the parent harness. The policy remains authored in the route; no state
// name is compiled into the hook.
func (s Spec) EditCapableStates() []string {
	var out []string
	for _, st := range s.States {
		if st.IsPerforming() && !st.IsAugmentation() && st.Agent == "executor" {
			out = append(out, st.Name)
		}
	}
	return out
}

// IsEditCapableState reports whether name is a spine performing node allocated
// to the in-loop executor. Unknown names are not edit-capable.
func (s Spec) IsEditCapableState(name string) bool {
	for _, st := range s.States {
		if st.Name == name {
			return st.IsPerforming() && !st.IsAugmentation() && st.Agent == "executor"
		}
	}
	return false
}

// StateAgent returns the node's agent allocation and whether the state exists.
// An empty agent on a declared state is distinguishable from an unknown state.
func (s Spec) StateAgent(name string) (string, bool) {
	for _, st := range s.States {
		if st.Name == name {
			return st.Agent, true
		}
	}
	return "", false
}

// NonTerminalEngagingStates returns all states that are neither the start state
// (Shape Mdiamond) nor a terminal state (Shape Msquare), nor a cancel/exception
// sink (agent=reviewer with no outgoing edges). This reads the Spec's own
// markers — no hardcoded state names — so the hook's engagement check is
// configuration-over-code (sty_f3d5d4b8).
func (s Spec) NonTerminalEngagingStates() []string {
	var out []string
	for _, st := range s.States {
		if !s.isEngaging(st) {
			continue
		}
		out = append(out, st.Name)
	}
	return out
}

// isEngaging reports whether a state is a non-terminal engaging state: it is
// neither the start state (shape=Mdiamond), nor a terminal state (shape=Msquare),
// nor any agent=reviewer role state, nor an augmentation annotation
// (sty_8225d8a5). Reviewer-role states are never engaged work (edit/commit
// gates): that covers cancel sinks AND park states that keep an outgoing resume
// edge (authored as agent=reviewer so a parked story is not engaged —
// config-over-code, no state-name literals).
func (s Spec) isEngaging(st State) bool {
	// Start marker: shape=Mdiamond
	if st.Shape == "Mdiamond" {
		return false
	}
	// Terminal marker: shape=Msquare
	if st.Shape == "Msquare" {
		return false
	}
	// Reviewer role: not engaged (cancel sinks, park/resume nodes, edge-less gates).
	if st.Agent == "reviewer" {
		return false
	}
	// Step-summary declaration: narrator only, never an in-flight status
	// (even when agent= names a non-literal reviewer binding — sty_8ee40f94).
	if st.IsSummariser() {
		return false
	}
	// Scoped named judges (on= + skill, agent not a performer token) are gates,
	// not in-flight statuses — same class as agent=reviewer with on= (sty_8ee40f94).
	if len(st.On) > 0 && st.Skill != "" && !st.IsPerforming() {
		return false
	}
	// Augmentation annotations are not lifecycle statuses (sty_8225d8a5).
	if st.IsAugmentation() {
		return false
	}
	return true
}

// IsPerformingState reports whether the named state exists and performs work.
// An unknown name is not performing (false). Used by the dispatch lock-guard to
// verify a named agent's FROM state is genuinely engaged.
func (s Spec) IsPerformingState(name string) bool {
	for _, st := range s.States {
		if st.Name == name {
			return st.IsPerforming()
		}
	}
	return false
}

// IsTerminalState reports whether name is a terminal success marker (shape=Msquare).
// Shape-derived — no hardcoded "done" (sty_8426b9c0 release keying).
func (s Spec) IsTerminalState(name string) bool {
	for _, st := range s.States {
		if st.Name == name {
			return st.Shape == "Msquare"
		}
	}
	return false
}

// IsParkState reports whether name is a reviewer-role non-start state (cancel
// sinks, blocked/park steps). Shape/role-derived so lease release keys to the
// route rather than status string literals (sty_8426b9c0).
func (s Spec) IsParkState(name string) bool {
	for _, st := range s.States {
		if st.Name == name {
			return st.Agent == "reviewer" && st.Shape != "Mdiamond"
		}
	}
	return false
}

// Advance is one offered onward transition from a performing state — target
// status and the reviewer gate skills the route declares on entry to it
// (sty_e16a2cd7). Gates is Transition.Skills.
type Advance struct {
	To    string
	Gates []string
}

// advanceOptionsCap bounds how many forward targets a prompt injection names.
// Two is enough for an unusual fork; more would blow the UserPromptSubmit budget.
const advanceOptionsCap = 2

// AdvanceOptions returns the forward, non-terminal, non-park successors of from,
// sorted by To and capped at advanceOptionsCap. Empty when none survive
// (sty_e16a2cd7). Pure graph semantics:
//
//  1. Drop IsTerminalState / IsParkState targets (shape=Msquare / agent=reviewer).
//  2. An edge from→to is forward when there EXISTS a shape=Msquare terminal T
//     such that dist_T[to] < dist_T[from] (per-terminal reverse shortest-path).
//     Combining all terminals into one distance map would collapse every
//     performing state to dist 1 via a cancel Msquare sink and silence the
//     genuine spine; the existential check keeps the spine when any success
//     terminal still measures progress. Recovery edges (release→in_progress)
//     fail every terminal's distance test and are never recommended.
//
// When every surviving edge is terminal, park, or a back-edge, the result is
// empty — callers fall back to their static text rather than inventing a step.
func (s Spec) AdvanceOptions(from string) []Advance {
	var terminals []string
	for _, st := range s.States {
		if st.Shape == "Msquare" {
			terminals = append(terminals, st.Name)
		}
	}
	// Per-terminal reverse distances — shared reverseDistFrom with doneReachable.
	dists := make([]map[string]int, 0, len(terminals))
	for _, t := range terminals {
		dists = append(dists, s.reverseDistFrom([]string{t}))
	}
	isForward := func(to string) bool {
		for _, dist := range dists {
			fromDist, fromOK := dist[from]
			toDist, toOK := dist[to]
			if fromOK && toOK && toDist < fromDist {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []Advance
	for _, tr := range s.Transitions {
		if tr.From != from || seen[tr.To] {
			continue
		}
		seen[tr.To] = true
		if s.IsTerminalState(tr.To) || s.IsParkState(tr.To) {
			continue
		}
		if !isForward(tr.To) {
			continue
		}
		out = append(out, Advance{To: tr.To, Gates: tr.Skills})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To < out[j].To })
	if len(out) > advanceOptionsCap {
		out = out[:advanceOptionsCap]
	}
	return out
}

// reverseDistFrom returns shortest-path distances walking edges in reverse from
// the given seeds (distance 0 at each seed). States not reverse-reachable are
// absent from the map.
func (s Spec) reverseDistFrom(seeds []string) map[string]int {
	rev := map[string][]string{}
	for _, tr := range s.Transitions {
		rev[tr.To] = append(rev[tr.To], tr.From)
	}
	dist := map[string]int{}
	var queue []string
	for _, seed := range seeds {
		if seed == "" {
			continue
		}
		if _, ok := dist[seed]; ok {
			continue
		}
		dist[seed] = 0
		queue = append(queue, seed)
	}
	for head := 0; head < len(queue); head++ {
		n := queue[head]
		d := dist[n]
		for _, from := range rev[n] {
			if _, ok := dist[from]; ok {
				continue
			}
			dist[from] = d + 1
			queue = append(queue, from)
		}
	}
	return dist
}

// ExecutorPathToDoneSkills returns every executor skill on a path to done,
// including ALL augmentation skills regardless of applies_to — used by structure
// validate so a declared code-ui must exist even when no story is in scope.
// Empty when no "done". See ExecutorPathToDoneSkillsFor for the tag-filtered
// engagement form (sty_8225d8a5).
func (s Spec) ExecutorPathToDoneSkills() []string {
	return s.executorPathSkills(nil, true)
}

// ExecutorPathToDoneSkillsFor returns executor skills on a path to done for a
// story with the given tags: spine performing skills plus only those
// augmentations whose applies_to matches (sty_8225d8a5). A missing code-ui
// therefore hard-blocks engagement of a surface:ui story and does NOT block a
// surface:cli story — the wasted-work trap stays surface-aware.
func (s Spec) ExecutorPathToDoneSkillsFor(tags []string) []string {
	return s.executorPathSkills(tags, false)
}

// executorPathSkills is the shared implementation. allAugmentations includes
// every augmentation skill (structure validate); otherwise applies_to is filtered
// by tagsMatchAppliesTo (shared with scoped reviewers — sty_c6d093c8).
func (s Spec) executorPathSkills(tags []string, allAugmentations bool) []string {
	reach := s.doneReachable()
	if len(reach) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, st := range s.States {
		if st.IsAugmentation() {
			if st.Skill == "" {
				continue
			}
			// Augment only if the target spine state can still reach done.
			targetsDone := false
			for _, target := range st.On {
				if reach[target] || target == "*" {
					targetsDone = true
					break
				}
			}
			if !targetsDone {
				continue
			}
			if allAugmentations || tagsMatchAppliesTo(st.AppliesTo, tags) {
				set[st.Skill] = true
			}
			continue
		}
		if st.IsPerforming() && st.Skill != "" && reach[st.Name] {
			set[st.Skill] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ExecutorSkillsFor returns the ordered rubrics for performing state `name` for
// a story with tags: the spine node's skill first (if any), then matching
// augmentation skills in declaration order (deterministic, additive —
// sty_8225d8a5). Empty when the state is unknown, not a performing spine node,
// or has no skills.
func (s Spec) ExecutorSkillsFor(name string, tags []string) []string {
	var base string
	foundSpine := false
	for _, st := range s.States {
		if st.Name == name && st.IsPerforming() && !st.IsAugmentation() {
			base = st.Skill
			foundSpine = true
			break
		}
	}
	if !foundSpine {
		return nil
	}
	var augs []string
	for _, st := range s.States {
		if !st.IsAugmentation() || st.Skill == "" {
			continue
		}
		if !(containsStr(st.On, "*") || containsStr(st.On, name)) {
			continue
		}
		if !tagsMatchAppliesTo(st.AppliesTo, tags) {
			continue
		}
		augs = append(augs, st.Skill)
	}
	var out []string
	if base != "" {
		out = append(out, base)
	}
	return append(out, augs...)
}

// DefaultParallelCap is the concurrency cap when an edge sets parallel=true
// without a numeric limit (sty_4f0a15db). Bounded because reviewers share backends.
const DefaultParallelCap = 4

// Transition is a directed edge. Skills are the reviewer gates admitting entry to
// the target node, in order (empty = ungated); Skill mirrors the first for
// single-reviewer back-compat. An edge declares its gate(s) either edge-centric
// (`reviewer_skill="a,b"`) or in the NODE-CONSISTENT form
// (`agent=reviewer, prompt="@skill:a"`) — the same vocabulary a reviewer node
// uses (sty_be67919a); reviewer_skill wins when both are present.
type Transition struct {
	From   string
	To     string
	Skill  string
	Skills []string
	// Agent is the agents.toml section that runs this edge's gate (agent=<name>).
	// Empty means [reviewer]. Model= on edges is superseded (sty_a476a2f8).
	Agent string
	// Parallel is the opt-in concurrency cap for this edge's reviewer list
	// (sty_4f0a15db). 0 = sequential first-reject short-circuit (today's default);
	// parallel=true → DefaultParallelCap; parallel=N (N≥1) → cap N. Edge-only —
	// not a node attribute (scoped nodes join many transitions).
	Parallel int
}

// Spec is the parsed lifecycle: states and gated transitions.
type Spec struct {
	States      []State
	Transitions []Transition
	// AttrProblems are named parse-time attribute issues (unknown keys,
	// applies_to on an edge) carried to Validate. Parse returns only ok/not-ok
	// for the fence, so it cannot reject itself — Validate appends these
	// (sty_c6d093c8). Empty on a clean graph.
	AttrProblems []string
	// AttrWarnings are non-fatal parse-time notes (e.g. superseded model=).
	// Surfaces via DocWarnings / workflow validate WARN lines (sty_a476a2f8).
	AttrWarnings []string
}

// Declares reports whether the route declares a directed transition from→to
// (sty_ebd3d666). Recovery, park, and cancel edges count when declared.
// Alias of HasEdge for call-site readability.
func (s Spec) Declares(from, to string) bool { return s.HasEdge(from, to) }

// HasEdge reports whether the route declares a directed transition from→to.
func (s Spec) HasEdge(from, to string) bool {
	for _, tr := range s.Transitions {
		if tr.From == from && tr.To == to {
			return true
		}
	}
	return false
}

// Successors returns the distinct target states of edges leaving from, sorted
// for stable agent-facing refuse messages (sty_ebd3d666).
func (s Spec) Successors(from string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tr := range s.Transitions {
		if tr.From != from || seen[tr.To] {
			continue
		}
		seen[tr.To] = true
		out = append(out, tr.To)
	}
	sort.Strings(out)
	return out
}

// splitCSV splits a comma-separated attribute value into trimmed, non-empty
// tokens (e.g. on="in_progress, done" → ["in_progress","done"]). Returns nil when
// empty.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitCSVSkills splits a comma-separated reviewer_skill value into skill names,
// stripping any per-token `@skill:` prefix (e.g. reviewer_skill="a,@skill:b").
func splitCSVSkills(s string) []string {
	out := splitCSV(s)
	for i, v := range out {
		out[i] = strings.TrimPrefix(v, "@skill:")
	}
	return out
}

// first returns the first element of ss, or "" when empty.
func first(ss []string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return ""
}

// containsStr reports whether ss contains v.
func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
