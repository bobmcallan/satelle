package structure

import "testing"

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
	if p := Story("Add X", "Make the thing do X", "1. it does X"); len(p) != 0 {
		t.Errorf("well-formed story should pass, got %v", p)
	}
	cases := []struct {
		name                    string
		title, body, acceptance string
	}{
		{"empty title", "", "goal", "1. a"},
		{"empty body", "T", "", "1. a"},
		{"body restates title", "Same", "same", "1. a"},
		{"no numbered AC", "T", "goal", "do it well"},
	}
	for _, c := range cases {
		if p := Story(c.title, c.body, c.acceptance); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

func TestSkill(t *testing.T) {
	rubric := "---\nname: my-skill\nkind: skill\ndescription: does a thing\n---\n\n# My skill\n\nDo the thing carefully."
	if p := Doc("skills", "my-skill", rubric, nil); len(p) != 0 {
		t.Errorf("well-formed rubric skill should pass, got %v", p)
	}
	check := "---\nname: my-check\nkind: skill\ndescription: a check\n---\n\n```check\ngo test ./...\n```"
	if p := Doc("skills", "my-check", check, nil); len(p) != 0 {
		t.Errorf("self-contained check skill should pass, got %v", p)
	}
	bad := []struct {
		name, slug, body string
	}{
		{"no frontmatter", "x", "# x\nbody"},
		{"wrong kind", "x", "---\nname: x\nkind: workflow\ndescription: d\n---\nbody"},
		{"name mismatch", "x", "---\nname: y\nkind: skill\ndescription: d\n---\nbody"},
		{"no description", "x", "---\nname: x\nkind: skill\n---\nbody"},
		{"no definition", "x", "---\nname: x\nkind: skill\ndescription: d\n---\n\n# x\n"},
	}
	for _, c := range bad {
		if p := Doc("skills", c.slug, c.body, nil); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

func TestPrinciple(t *testing.T) {
	good := "---\nname: my-principle\nkind: principle\ndescription: a rule\ntags: [kind:principle]\n---\n\n# My principle\n\nThe rule and why it matters."
	if p := Doc("principles", "my-principle", good, nil); len(p) != 0 {
		t.Errorf("well-formed principle should pass, got %v", p)
	}
	bad := []struct{ name, slug, body string }{
		{"stub body", "x", "---\nname: x\nkind: principle\ndescription: d\ntags: [a]\n---\n\n# x\n"},
		{"no tags", "x", "---\nname: x\nkind: principle\ndescription: d\n---\n\nprose here"},
		{"wrong kind", "x", "---\nname: x\nkind: skill\ndescription: d\ntags: [a]\n---\n\nprose"},
	}
	for _, c := range bad {
		if p := Doc("principles", c.slug, c.body, nil); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

const validWF = "---\nname: wf-x\nkind: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: test lifecycle\n---\n\n# wf\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  in_progress [actor=executor]\n  done [shape=Msquare, actor=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> in_progress\n  in_progress -> done\n}\n```"

func TestWorkflow(t *testing.T) {
	resolveAll := func(string) bool { return true }
	if p := Doc("workflows", "wf-x", validWF, resolveAll); len(p) != 0 {
		t.Errorf("valid workflow should pass, got %v", p)
	}
	// Missing applies_to.
	noApplies := "---\nname: wf-x\nkind: workflow\nscope: project\ndescription: d\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare, actor=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> done\n}\n```"
	if p := Doc("workflows", "wf-x", noApplies, resolveAll); len(p) == 0 {
		t.Error("missing applies_to: want reject, got pass")
	}
	// Non-backlog start.
	badStart := "---\nname: wf-x\nkind: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n```dot\ndigraph x {\n  open [shape=Mdiamond]\n  done [shape=Msquare, actor=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  open -> done\n}\n```"
	if p := Doc("workflows", "wf-x", badStart, resolveAll); len(p) == 0 {
		t.Error("non-backlog start: want reject, got pass")
	}
	// Unresolved executor-step skill.
	execSkill := "---\nname: wf-x\nkind: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n```dot\ndigraph x {\n  backlog [shape=Mdiamond]\n  in_progress [actor=executor, prompt=\"@skill:missing-skill\"]\n  done [shape=Msquare, actor=reviewer, prompt=\"@skill:satelle-story-done-review\"]\n  backlog -> in_progress\n  in_progress -> done\n}\n```"
	if p := Doc("workflows", "wf-x", execSkill, func(s string) bool { return s != "missing-skill" }); len(p) == 0 {
		t.Error("unresolved executor skill: want reject, got pass")
	}
}
