package config

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// The embedded lifecycle is one DERIVED ROUTE in two halves — workflows/done.md
// and workflows/step.md — rather than four DOT graphs (sty_3795e7f6). The
// corpus tests in this package used to reach for `wfdot.Parse` per workflow
// file; these helpers give them the same two answers (what states does the
// shipped lifecycle have, what skills does it bind) off the route grammar, so
// there is one place that knows the shipped representation.

// embeddedRouteHalves returns the two shipped halves, empty when either is
// absent.
func embeddedRouteHalves() (done, step string) {
	for _, d := range EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		switch d.Name {
		case "done":
			done = d.Body
		case "step":
			step = d.Body
		}
	}
	if done == "" || step == "" {
		return "", ""
	}
	return done, step
}

// embeddedRouteSpecs derives one Spec per category the shipped route declares.
func embeddedRouteSpecs() map[string]wfdot.Spec {
	done, step := embeddedRouteHalves()
	if done == "" {
		return nil
	}
	lists, err := wfdot.ParseDone(done)
	if err != nil {
		return nil
	}
	cat, err := wfdot.ParseSteps(step)
	if err != nil {
		return nil
	}
	out := map[string]wfdot.Spec{}
	for _, l := range lists {
		spec, err := wfdot.BuildRoute(l, cat, nil)
		if err != nil {
			continue
		}
		out[l.Category] = spec
	}
	return out
}

// routeSourceSkillRefs returns every skill one half of the route binds — a
// step's executor rubrics and entry reviewers, an always-on gate, a park or
// cancel gate. A body that is not route grammar yields nothing.
func routeSourceSkillRefs(body string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if lists, err := wfdot.ParseDone(body); err == nil {
		for _, l := range lists {
			add(l.ParkGate, l.CancelGate, l.ParkAdvisorSkill)
		}
	}
	if cat, err := wfdot.ParseSteps(body); err == nil {
		for _, st := range cat.Steps {
			add(st.Skills...)
			add(st.Reviewers...)
			add(st.AdvisorSkill)
		}
		for _, g := range cat.Gates {
			add(g.Skill)
		}
	}
	return out
}

// TestEmbeddedRouteIsTheOnlyLifecycle pins the shipped representation (AC1 and
// AC7 of sty_3795e7f6): the binary ships exactly the two route halves as
// workflows, and neither is a DOT graph. It is the deterministic guard against
// a graph creeping back into the embed tree.
func TestEmbeddedRouteIsTheOnlyLifecycle(t *testing.T) {
	var names []string
	for _, d := range EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		names = append(names, d.Name)
		if _, isDOT := wfdot.Parse(d.Body); isDOT {
			t.Errorf("embedded workflow %q carries a DOT graph — the shipped lifecycle is a derived route", d.Name)
		}
	}
	if len(names) != 2 {
		t.Fatalf("embedded workflows = %v, want exactly [done step]", names)
	}
	for _, want := range []string{"done", "step"} {
		var found bool
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("embedded workflows %v are missing %q — one half is not a route", names, want)
		}
	}
}

// TestEmbeddedRouteBuildsForEveryDeclaredCategory: every `## <category>` section
// derives a valid route. A section whose obligations nothing discharges is a
// construction error the operator would only meet at the first transition.
func TestEmbeddedRouteBuildsForEveryDeclaredCategory(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" {
		t.Fatal("the binary ships no route source")
	}
	lists, err := wfdot.ParseDone(done)
	if err != nil {
		t.Fatalf("embedded done.md does not parse: %v", err)
	}
	if len(lists) == 0 {
		t.Fatal("embedded done.md declares no category")
	}
	for _, l := range lists {
		spec, err := wfdot.ParseRoute(done, step, l.Category, nil)
		if err != nil {
			t.Errorf("category %q does not derive: %v", l.Category, err)
			continue
		}
		if problems := wfdot.Validate(spec); len(problems) != 0 {
			t.Errorf("category %q does not validate: %v", l.Category, problems)
		}
		if start := spec.Start(); start != "backlog" {
			t.Errorf("category %q starts at %q, want backlog", l.Category, start)
		}
	}
}

// TestEmbeddedRouteSkillsAllShip: every rubric the shipped route names resolves
// to an embedded skill. A dangling @skill: in the DEFAULT lifecycle would block
// a fresh repo with no substrate of its own.
func TestEmbeddedRouteSkillsAllShip(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" {
		t.Fatal("the binary ships no route source")
	}
	skills := map[string]bool{}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" {
			skills[d.Name] = true
		}
	}
	refs := append(routeSourceSkillRefs(done), routeSourceSkillRefs(step)...)
	refs = append(refs, fmScalarLine(splitLines(done), "create_review"))
	if len(refs) == 0 {
		t.Fatal("the shipped route binds no skills")
	}
	for _, sk := range refs {
		if sk == "" {
			continue
		}
		if !skills[sk] {
			t.Errorf("the shipped route binds %q, which is not an embedded skill", sk)
		}
	}
}

// splitLines is the frontmatter slice fmScalarLine expects.
func splitLines(body string) []string {
	fm, _, ok := splitFrontmatter(body)
	if !ok {
		return nil
	}
	return fm
}
