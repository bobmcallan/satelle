// Package wfequiv diffs two workflow Specs for BEHAVIOURAL equivalence — the
// go/no-go instrument for retiring the authored DOT in favour of a route derived
// from a declaration of done (epic sty_c754b5c8, story sty_c6184eaa).
//
// The currency is wfdot.Spec, not DOT text. The epic keeps Spec and its ~25
// methods and makes the obligation list a SECOND CONSTRUCTOR onto the same type,
// so a checker that never learns DOT and never learns obligations stays usable
// unchanged as the migration child's safety net.
//
// Equivalence is asserted over METHOD OUTPUTS, not struct fields. Spec.States
// carries authoring detail (Shape, comment-adjacent attributes) whose equality is
// not required for the engine to behave identically; diffing the structs would
// manufacture divergence and make the checker useless. What matters is what
// PerformingStates, ScopedReviewersSplit, ExecutorSkillsFor and friends RETURN,
// because that is what the engine, the seat and the edit gate consume.
package wfequiv

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Report is a divergence report, one slice per dimension so a failure names WHICH
// dimension moved rather than dumping an undifferentiated blob.
type Report struct {
	// Path is reachable-path divergence: start state, edge set, and the
	// path-shaped predicates the engine and edit gate actually consume.
	Path []string
	// Gates is per-edge reviewer gate divergence (skills, binding, concurrency).
	Gates []string
	// Scoped is per-target edge-less always-on gate divergence, per tag set.
	Scoped []string
	// Executor is executor-rubric divergence, per tag set.
	Executor []string
}

// Empty reports whether the two Specs are behaviourally equivalent.
func (r Report) Empty() bool {
	return len(r.Path) == 0 && len(r.Gates) == 0 && len(r.Scoped) == 0 && len(r.Executor) == 0
}

// Count is the total number of divergences across every dimension.
func (r Report) Count() int {
	return len(r.Path) + len(r.Gates) + len(r.Scoped) + len(r.Executor)
}

// String renders the report for a human — stable ordering so it can be goldened.
func (r Report) String() string {
	if r.Empty() {
		return "equivalent: no divergence in path, gates, scoped reviewers, or executor skills\n"
	}
	var b strings.Builder
	for _, d := range []struct {
		name  string
		lines []string
	}{
		{"PATH", r.Path},
		{"GATES", r.Gates},
		{"SCOPED", r.Scoped},
		{"EXECUTOR", r.Executor},
	} {
		if len(d.lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s (%d)\n", d.name, len(d.lines))
		for _, l := range d.lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	return b.String()
}

// DefaultTagSets is the tag matrix Diff uses. Three of the four dimensions are
// tag-dependent because applies_to filters both scoped reviewers and executor
// augmentations: a single tag-less comparison would pass while silently dropping
// the project workflow's surface:ui design gate. The matrix is not optional.
var DefaultTagSets = [][]string{nil, {"surface:cli"}, {"surface:ui"}}

// Diff compares want (the authored graph) against got (the derived route) over
// DefaultTagSets.
func Diff(want, got wfdot.Spec) Report {
	return DiffFor(want, got, DefaultTagSets)
}

// DiffFor is Diff with an explicit tag matrix. The tag-independent dimensions
// (path, gates) are compared once; scoped and executor are compared per tag set.
func DiffFor(want, got wfdot.Spec, tagSets [][]string) Report {
	var r Report
	r.Path = diffPath(want, got)
	r.Gates = diffGates(want, got)
	if len(tagSets) == 0 {
		tagSets = [][]string{nil}
	}
	for _, tags := range tagSets {
		r.Scoped = append(r.Scoped, diffScoped(want, got, tags)...)
		r.Executor = append(r.Executor, diffExecutor(want, got, tags)...)
	}
	return r
}

// diffPath compares the shape of the lifecycle: the start state, the edge set,
// and every path-shaped predicate a caller keys behaviour off. Comparing node
// names alone is not enough — "same path" has to mean the predicates answer the
// same way, because those answers are what the engine and edit gate consume.
func diffPath(want, got wfdot.Spec) []string {
	var out []string
	if w, g := want.Start(), got.Start(); w != g {
		out = append(out, fmt.Sprintf("start state: want %q, got %q", w, g))
	}
	out = append(out, diffStringSets("edge", edgeSet(want), edgeSet(got))...)
	out = append(out, diffOrderedList("PerformingStates", want.PerformingStates(), got.PerformingStates())...)
	out = append(out, diffOrderedList("EditCapableStates", want.EditCapableStates(), got.EditCapableStates())...)
	out = append(out, diffOrderedList("NonTerminalEngagingStates",
		want.NonTerminalEngagingStates(), got.NonTerminalEngagingStates())...)
	// The step summariser carries no on=, so the scoped dimension never sees it.
	// Compared explicitly rather than by node name: losing a mandatory summariser
	// would otherwise be invisible.
	if w, g := summaryBinding(want), summaryBinding(got); w != g {
		out = append(out, fmt.Sprintf("step summary: want %s, got %s", w, g))
	}

	for _, name := range unionStates(want, got) {
		wa, wok := want.StateAgent(name)
		ga, gok := got.StateAgent(name)
		if wok != gok {
			out = append(out, fmt.Sprintf("state %q: declared in want=%t, got=%t", name, wok, gok))
			continue
		}
		if wa != ga {
			out = append(out, fmt.Sprintf("state %q agent: want %q, got %q", name, wa, ga))
		}
		// on_enter_agent was compared here while it was LIVE dispatch: a
		// representation that could not carry it had to be REPORTED, not quietly
		// blessed. Flat dispatch retired it (sty_05a5e203) — entry no longer fires
		// an agent in EITHER representation — so there is nothing left to compare.
		// An advisor is an instruction to the orchestrator carried on the route,
		// not topology, and topology is what this checker exists to compare.
		for _, p := range []struct {
			label string
			w, g  bool
		}{
			{"IsTerminalState", want.IsTerminalState(name), got.IsTerminalState(name)},
			{"IsParkState", want.IsParkState(name), got.IsParkState(name)},
			{"IsPerformingState", want.IsPerformingState(name), got.IsPerformingState(name)},
			{"IsEditCapableState", want.IsEditCapableState(name), got.IsEditCapableState(name)},
		} {
			if p.w != p.g {
				out = append(out, fmt.Sprintf("state %q %s: want %t, got %t", name, p.label, p.w, p.g))
			}
		}
		out = append(out, diffOrderedList(fmt.Sprintf("Successors(%s)", name),
			want.Successors(name), got.Successors(name))...)
		out = append(out, diffOrderedList(fmt.Sprintf("AdvanceOptions(%s)", name),
			advanceStrings(want.AdvanceOptions(name)), advanceStrings(got.AdvanceOptions(name)))...)
	}
	return out
}

// diffGates compares the reviewer gates on every edge in the union of both edge
// sets. Skill and Skills are normalised together so a single-vs-CSV authoring
// difference is not reported as divergence; Agent and Parallel are compared
// because the binding and the concurrency cap are part of the gating contract,
// not decoration.
func diffGates(want, got wfdot.Spec) []string {
	w := transitionIndex(want)
	g := transitionIndex(got)
	var keys []string
	seen := map[string]bool{}
	for k := range w {
		if !seen[k] {
			seen[k], keys = true, append(keys, k)
		}
	}
	for k := range g {
		if !seen[k] {
			seen[k], keys = true, append(keys, k)
		}
	}
	sort.Strings(keys)

	var out []string
	for _, k := range keys {
		wt, wok := w[k]
		gt, gok := g[k]
		if !wok || !gok {
			// Edge presence is a PATH divergence, already reported there.
			continue
		}
		if a, b := gateSkills(wt), gateSkills(gt); !equalStrings(a, b) {
			out = append(out, fmt.Sprintf("edge %s gates: want [%s], got [%s]",
				k, strings.Join(a, " "), strings.Join(b, " ")))
		}
		if wt.Agent != gt.Agent {
			out = append(out, fmt.Sprintf("edge %s gate binding: want %q, got %q", k, wt.Agent, gt.Agent))
		}
		if wt.Parallel != gt.Parallel {
			out = append(out, fmt.Sprintf("edge %s parallel: want %d, got %d", k, wt.Parallel, gt.Parallel))
		}
	}
	return out
}

// diffScoped compares the edge-less always-on gates enqueued (and deliberately
// skipped) on entry to every state, for one tag set. The skipped list matters as
// much as the enqueued one: a derived route that never DECLARES a surface gate is
// not equivalent to one that declares it and filters it out, even though both
// enqueue nothing.
func diffScoped(want, got wfdot.Spec, tags []string) []string {
	var out []string
	label := tagLabel(tags)
	for _, name := range unionStates(want, got) {
		we, ws := want.ScopedReviewersSplit(name, tags)
		ge, gs := got.ScopedReviewersSplit(name, tags)
		out = append(out, diffOrderedList(
			fmt.Sprintf("scoped-enqueued(%s, %s)", name, label),
			scopedStrings(we), scopedStrings(ge))...)
		out = append(out, diffOrderedList(
			fmt.Sprintf("scoped-skipped(%s, %s)", name, label),
			scopedStrings(ws), scopedStrings(gs))...)
	}
	return out
}

// diffExecutor compares the executor rubrics: the whole path-to-done set (both
// the all-augmentations form structure validate uses and the tag-filtered form
// engagement uses) and the ordered per-state rubric list a dispatched step
// actually receives. ExecutorSkillsFor is declaration-ordered by contract, so it
// is compared in order, not as a set.
func diffExecutor(want, got wfdot.Spec, tags []string) []string {
	var out []string
	label := tagLabel(tags)
	out = append(out, diffOrderedList("ExecutorPathToDoneSkills",
		want.ExecutorPathToDoneSkills(), got.ExecutorPathToDoneSkills())...)
	out = append(out, diffOrderedList(fmt.Sprintf("ExecutorPathToDoneSkillsFor(%s)", label),
		want.ExecutorPathToDoneSkillsFor(tags), got.ExecutorPathToDoneSkillsFor(tags))...)
	for _, name := range unionStates(want, got) {
		out = append(out, diffOrderedList(fmt.Sprintf("ExecutorSkillsFor(%s, %s)", name, label),
			want.ExecutorSkillsFor(name, tags), got.ExecutorSkillsFor(name, tags))...)
	}
	return out
}

// --- helpers ---

func edgeSet(s wfdot.Spec) []string {
	out := make([]string, 0, len(s.Transitions))
	seen := map[string]bool{}
	for _, tr := range s.Transitions {
		k := tr.From + "->" + tr.To
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func transitionIndex(s wfdot.Spec) map[string]wfdot.Transition {
	out := map[string]wfdot.Transition{}
	for _, tr := range s.Transitions {
		out[tr.From+"->"+tr.To] = tr
	}
	return out
}

// gateSkills normalises a transition's gate list: Skills when present, else the
// single Skill. Sorted, because an edge's reviewer CSV is an all-must-accept set
// (the engine runs them in order but no gate depends on another's position).
func gateSkills(tr wfdot.Transition) []string {
	out := append([]string(nil), tr.Skills...)
	if len(out) == 0 && tr.Skill != "" {
		out = []string{tr.Skill}
	}
	sort.Strings(out)
	return out
}

func summaryBinding(s wfdot.Spec) string {
	agent, declared, mandatory := s.StepSummaryBinding()
	if !declared {
		return "undeclared"
	}
	if agent == "" {
		agent = "reviewer"
	}
	return fmt.Sprintf("%s(mandatory=%t)", agent, mandatory)
}

func scopedStrings(rs []wfdot.ScopedReviewer) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.Agent == "" {
			out = append(out, r.Skill)
			continue
		}
		out = append(out, r.Skill+"@"+r.Agent)
	}
	return out
}

func advanceStrings(as []wfdot.Advance) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.To+"["+strings.Join(a.Gates, " ")+"]")
	}
	return out
}

// unionStates is every LIFECYCLE state name declared by either Spec, sorted — so
// a state present in one and absent from the other is still probed on both.
//
// Edge-less gate declarations are excluded. A gate is identified by its SKILL,
// not by the node name it happens to be authored under: nothing in the engine
// keys off that name, so comparing it manufactures divergence for a rename. The
// gates themselves are compared by skill in the scoped dimension, which is where
// a genuinely lost gate shows up.
func unionStates(want, got wfdot.Spec) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []wfdot.Spec{want, got} {
		for _, st := range s.States {
			if isGateDeclaration(st) || seen[st.Name] {
				continue
			}
			seen[st.Name], out = true, append(out, st.Name)
		}
	}
	sort.Strings(out)
	return out
}

// isGateDeclaration reports whether a node is an edge-less gate or summariser
// rather than a status a story can hold.
func isGateDeclaration(st wfdot.State) bool {
	return st.IsSummariser() || (len(st.On) > 0 && st.Skill != "")
}

// diffOrderedList reports divergence in an ORDER-SENSITIVE list. Used for every
// method whose contract fixes the order (ExecutorSkillsFor is spine-then-
// augmentations in declaration order; ScopedReviewersSplit sorts internally).
func diffOrderedList(label string, want, got []string) []string {
	if equalStrings(want, got) {
		return nil
	}
	return []string{fmt.Sprintf("%s: want [%s], got [%s]",
		label, strings.Join(want, " "), strings.Join(got, " "))}
}

// diffStringSets reports per-element presence divergence between two sorted sets,
// so a missing edge is named individually rather than as one opaque list delta.
func diffStringSets(label string, want, got []string) []string {
	w := map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	g := map[string]bool{}
	for _, s := range got {
		g[s] = true
	}
	var out []string
	for _, s := range want {
		if !g[s] {
			out = append(out, fmt.Sprintf("%s %s: in want, missing from got", label, s))
		}
	}
	for _, s := range got {
		if !w[s] {
			out = append(out, fmt.Sprintf("%s %s: in got, missing from want", label, s))
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tagLabel(tags []string) string {
	if len(tags) == 0 {
		return "no-tags"
	}
	return strings.Join(tags, ",")
}
