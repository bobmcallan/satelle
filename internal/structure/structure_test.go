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
		// sty_1278fdd9: scope is a workflow field; principles use principles:session only.
		{"scope on principle", "x", "---\nname: x\ntype: principle\nscope: system\ndescription: d\ntags: [a]\n---\n\nprose here"},
	}
	for _, c := range bad {
		if p := Doc("principles", c.slug, c.body, nil); len(p) == 0 {
			t.Errorf("%s: want reject, got pass", c.name)
		}
	}
}

// TestWorkflow: a lifecycle is a DERIVED ROUTE, so the workflows kind admits
// exactly two docs — done.md and step.md — and anything else under the dir
// governs nothing and is reported as such (sty_d953c5d8). The route-source
// grammar checks live in TestRouteSource.
func TestWorkflow(t *testing.T) {
	resolveAll := func(string) bool { return true }
	notARoute := "---\nname: wf-x\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: d\n---\n\n# x\n"
	p := Doc("workflows", "wf-x", notARoute, resolveAll)
	if !hasProb(p, "not a route source") {
		t.Errorf("a workflows doc that is not a route source must say so, got %v", p)
	}
	// The remedy an agent can ACT on is the conversion guide, not a command that
	// only reports the conversion is outstanding (sty_d953c5d8).
	if !hasProb(p, "satelle help workflow-convert") {
		t.Errorf("the message must name the conversion guide, got %v", p)
	}
	// A doc with no frontmatter at all is still reported for that first.
	if p := Doc("workflows", "wf-x", "the body is not toml and has no frontmatter\n", resolveAll); !hasProb(p, "missing frontmatter") {
		t.Errorf("missing frontmatter: want that reject, got %v", p)
	}
}

// routeHalf renders one half of a derived route with the frontmatter the check
// requires, so a case only has to state the body it is about. A route source is
// TOML, so its frontmatter is a `[meta]` table rather than a `---` block
// (sty_81bb0dde) — emitting the YAML form here would hand the TOML parser a
// document it cannot read, and every case would fail on line 1 for the wrong
// reason.
func routeHalf(name, body string) string {
	return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"d\"\n\n" + body
}

// TestRouteSource pins the route-source grammar checks, and in particular WHICH
// unresolved skill is a hard failure.
//
// Only the EXECUTOR rubric is. A step whose rubric is missing cannot be
// performed, so the story can never reach its terminal state (sty_09ef53d6). A
// reviewer gate degrades to advisory when absent by design, and a repo
// mid-authoring writes its route before its gate skills — hard-failing those
// would make the ordinary authoring sequence impossible (sty_d59ec6a9). They
// surface as the WARN agentstep.WorkflowSkillProblems reports instead.
func TestRouteSource(t *testing.T) {
	none := func(string) bool { return false }
	all := func(string) bool { return true }

	step := routeHalf("step", `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["never-authored-review"]
reviewer_agent = "reviewer"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[[gate]]
skill = "never-authored-gate"
on = ["done"]
for = ["*"]
`)
	if p := Doc("workflows", "step", step, none); len(p) != 0 {
		t.Errorf("an unresolved REVIEWER gate must not be a structure failure, got %v", p)
	}

	exec := routeHalf("step", `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
skills = ["never-authored-rubric"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	if p := Doc("workflows", "step", exec, none); !hasProb(p, "executor-step skill never-authored-rubric") {
		t.Errorf("an unresolved EXECUTOR rubric must fail, got %v", p)
	}

	// A route source selects by done.toml's category tables, so its own applies_to
	// would be a second precedence rule.
	withApplies := routeHalf("done", "[\"*\"]\nobligations = [\"raised\"]\n")
	withApplies = strings.Replace(withApplies, "[meta]\n", "[meta]\napplies_to = [\"*\"]\n", 1)
	if p := Doc("workflows", "done", withApplies, all); !hasProb(p, "must not declare applies_to") {
		t.Errorf("a route source declaring applies_to must be rejected, got %v", p)
	}

	// An unresolved park/cancel gate is a gate too — reported, not a failure.
	done := routeHalf("done", `["*"]
obligations = ["raised"]
cancel = { state = "cancelled", gate = "never-authored-cancel" }
`)
	if p := Doc("workflows", "done", done, none); len(p) != 0 {
		t.Errorf("an unresolved cancel gate must not be a structure failure, got %v", p)
	}
	if p := Doc("workflows", "done", routeHalf("done", "[\"*\"]\n"), all); !hasProb(p, "declares no obligations") {
		t.Errorf("a section with no obligations must be reported, got %v", p)
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

func TestReviewerSkillContract(t *testing.T) {
	ok := "---\nname: r\ntype: skill\ndescription: d\n---\n\nReturn {\"decision\": \"accept\", \"notes\": \"…\"}.\n"
	if p := ReviewerSkillContract(ok); len(p) != 0 {
		t.Errorf("ok skill problems: %v", p)
	}
	bad := "---\nname: r\ntype: skill\ndescription: d\n---\n\nJust judge the story.\n"
	if p := ReviewerSkillContract(bad); len(p) == 0 {
		t.Error("expected problems for skill without decision/notes")
	}
	// Functional check exempt
	check := "---\nname: c\ntype: skill\ndescription: d\ncheck: true\n---\n\n```check\nexit 0\n```\n"
	if p := ReviewerSkillContract(check); len(p) != 0 {
		t.Errorf("check skill should be exempt: %v", p)
	}
}
