package structure

import (
	"strings"
	"testing"
)

func TestChecked(t *testing.T) {
	for _, k := range []string{"skills", "workflows", "principles"} {
		if !Checked(k) {
			t.Errorf("Checked(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"documents", "", "stories"} {
		if Checked(k) {
			t.Errorf("Checked(%q) = true, want false", k)
		}
	}
}

func TestStory(t *testing.T) {
	if p := Story("Add X", "Make the thing do X", "1. it does X", "feature"); len(p) != 0 {
		t.Errorf("well-formed story should pass, got %v", p)
	}
	cases := []struct {
		name                              string
		title, body, acceptance, category string
	}{
		{"empty title", "", "goal", "1. a", "feature"},
		{"empty body", "T", "", "1. a", "feature"},
		{"body restates title", "Same", "same", "1. a", "feature"},
		{"no numbered AC", "T", "goal", "do it well", "feature"},
		// category is a deterministic conformance rule (sty_af239840) — it selects
		// the governing workflow, so an empty one is a structural reject.
		{"empty category", "T", "goal", "1. a", ""},
	}
	for _, c := range cases {
		if p := Story(c.title, c.body, c.acceptance, c.category); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
	// The empty-category reject names the flag to pass (actionable message).
	if p := Story("T", "goal", "1. a", ""); len(p) != 1 || !strings.Contains(p[0], "--category") {
		t.Errorf("empty-category reject should name --category, got %v", p)
	}
}

func TestSkill(t *testing.T) {
	rubric := "---\nname: my-skill\ntype: skill\ndescription: does a thing\n---\n\n# My skill\n\nDo the thing carefully."
	if p := Doc("skills", "my-skill", rubric, nil); len(p) != 0 {
		t.Errorf("well-formed rubric skill should pass, got %v", p)
	}
	check := "---\nname: my-check\ntype: skill\ndescription: a check\n---\n\n```check\ngo test ./...\n```"
	if p := Doc("skills", "my-check", check, nil); len(p) != 0 {
		t.Errorf("self-contained check skill should pass, got %v", p)
	}
	bad := []struct {
		name, slug, body string
	}{
		{"no frontmatter", "x", "# x\nbody"},
		{"wrong kind", "x", "---\nname: x\ntype: workflow\ndescription: d\n---\nbody"},
		{"name mismatch", "x", "---\nname: y\ntype: skill\ndescription: d\n---\nbody"},
		{"no description", "x", "---\nname: x\ntype: skill\n---\nbody"},
		{"no definition", "x", "---\nname: x\ntype: skill\ndescription: d\n---\n\n# x\n"},
	}
	for _, c := range bad {
		if p := Doc("skills", c.slug, c.body, nil); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

func TestPrinciple(t *testing.T) {
	good := "---\nname: my-principle\ntype: principle\ndescription: a rule\ntags: [kind:principle]\n---\n\n# My principle\n\nThe rule and why it matters."
	if p := Doc("principles", "my-principle", good, nil); len(p) != 0 {
		t.Errorf("well-formed principle should pass, got %v", p)
	}
	bad := []struct{ name, slug, body string }{
		{"stub body", "x", "---\nname: x\ntype: principle\ndescription: d\ntags: [a]\n---\n\n# x\n"},
		{"no tags", "x", "---\nname: x\ntype: principle\ndescription: d\n---\n\nprose here"},
		{"wrong kind", "x", "---\nname: x\ntype: skill\ndescription: d\ntags: [a]\n---\n\nprose"},
	}
	for _, c := range bad {
		if p := Doc("principles", c.slug, c.body, nil); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

const validWF = "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: test lifecycle\n---\n\n# wf\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  in_progress [agent=executor]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> in_progress\n  in_progress -> done\n}\n```"

func TestWorkflow(t *testing.T) {
	resolveAll := func(string) bool { return true }
	if p := Doc("workflows", "wf-x", validWF, resolveAll); len(p) != 0 {
		t.Errorf("valid workflow should pass, got %v", p)
	}
	// Missing applies_to.
	noApplies := "---\nname: wf-x\ntype: workflow\nscope: project\ndescription: d\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> done\n}\n```"
	if p := Doc("workflows", "wf-x", noApplies, resolveAll); len(p) == 0 {
		t.Error("missing applies_to: want reject, got pass")
	}
	// Non-backlog start.
	badStart := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n```dot\ndigraph x {\n  open [shape=Mdiamond]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  open -> done\n}\n```"
	if p := Doc("workflows", "wf-x", badStart, resolveAll); len(p) == 0 {
		t.Error("non-backlog start: want reject, got pass")
	}
	// Unresolved executor-step skill.
	execSkill := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  in_progress [agent=executor, prompt=\"@skill:missing-skill\"]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> in_progress\n  in_progress -> done\n}\n```"
	if p := Doc("workflows", "wf-x", execSkill, func(s string) bool { return s != "missing-skill" }); len(p) == 0 {
		t.Error("unresolved executor skill: want reject, got pass")
	}
	// Deprecated actor= keyword is rejected (sty_7db2ed7d): the retired performer
	// keyword must fail validation with an actionable message, not silently drop a
	// node's performer.
	deprecated := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  in_progress [actor=executor]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> in_progress\n  in_progress -> done\n}\n```"
	if p := Doc("workflows", "wf-x", deprecated, resolveAll); !hasProb(p, `deprecated "actor"`) {
		t.Errorf("deprecated actor= keyword: want a reject naming it, got %v", p)
	}
	// Prose/DOT drift (sty_ca9f675f): a description whose lifecycle arrow-chain names
	// a state absent from the DOT is rejected.
	drift := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: moves backlog → commit_push → done\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> done\n}\n```"
	if p := Doc("workflows", "wf-x", drift, resolveAll); !hasProb(p, "prose/DOT drift") {
		t.Errorf("description naming a non-node state: want a drift reject, got %v", p)
	}
	// An aligned arrow-chain passes the drift guard.
	aligned := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: moves backlog → done\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare, agent=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> done\n}\n```"
	if p := Doc("workflows", "wf-x", aligned, resolveAll); len(p) != 0 {
		t.Errorf("aligned description should pass, got %v", p)
	}
}

func hasProb(ps []string, sub string) bool {
	for _, p := range ps {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}
