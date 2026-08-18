package wfroute

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// repoRoot walks up for go.mod so the fixtures resolve from the package dir.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("no repo root in this checkout")
	return ""
}

// derived builds a Spec through the DERIVED front door (done.toml + step.toml).
func derived(t *testing.T, tags []string) wfdot.Spec {
	t.Helper()
	root := repoRoot(t)
	done, err := os.ReadFile(filepath.Join(root, "internal", "wfdot", "testdata", "done.toml"))
	if err != nil {
		t.Fatalf("read done.toml: %v", err)
	}
	step, err := os.ReadFile(filepath.Join(root, "internal", "wfdot", "testdata", "step.toml"))
	if err != nil {
		t.Fatalf("read step.toml: %v", err)
	}
	spec, err := wfdot.ParseRoute(string(done), string(step), "feature", tags)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return spec
}

// TestRouteExposesEveryStepField (AC1): a step names its status, its obligation,
// its performer, its rubrics and its entry reviewers. All five, per step, or the
// route is not a substitute for reading the graph.
func TestRouteExposesEveryStepField(t *testing.T) {
	r := Build(derived(t, nil), "satelle-project-workflow", nil, nil)
	byName := map[string]Step{}
	for _, s := range r.Steps {
		byName[s.Status] = s
	}
	for _, want := range []string{"backlog", "plan", "in_progress", "integration", "release", "done"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("route is missing step %q; got %v", want, byName)
		}
	}
	if got := byName["in_progress"]; got.Obligation != "coded" || got.Agent != "executor" ||
		len(got.Skills) != 1 || got.Skills[0] != "code" {
		t.Errorf("in_progress = %+v; want obligation=coded agent=executor skills=[code]", got)
	}
	gates := skillsOf(byName["in_progress"].Reviewers)
	for _, want := range []string{"satelle-story-plan-review", "satelle-estimate-actual-review"} {
		if !contains(gates, want) {
			t.Errorf("in_progress entry gates = %v; want %q among them", gates, want)
		}
	}
	if !byName["done"].Terminal {
		t.Error("done must render as the route's terminal step")
	}
}

// TestRouteMarksTagScopedGates (AC1): a gate present only because the story
// carries a tag says so, and one filtered out for want of that tag is recorded
// as skipped — so "no gate" and "gate not for you" stay distinguishable.
func TestRouteMarksTagScopedGates(t *testing.T) {
	ui := stepAt(t, Build(derived(t, []string{"surface:ui"}), "wf", []string{"surface:ui"}, nil), "integration")
	var scoped *Reviewer
	for i, rv := range ui.Reviewers {
		if rv.Skill == "satelle-design-review" {
			scoped = &ui.Reviewers[i]
		}
	}
	if scoped == nil {
		t.Fatalf("surface:ui story is missing the design gate; got %v", skillsOf(ui.Reviewers))
	}
	if len(scoped.ByTag) == 0 || scoped.ByTag[0] != "surface:ui" {
		t.Errorf("design gate ByTag = %v; want [surface:ui] so the route says WHY it is present", scoped.ByTag)
	}

	cli := stepAt(t, Build(derived(t, []string{"surface:cli"}), "wf", []string{"surface:cli"}, nil), "integration")
	if contains(skillsOf(cli.Reviewers), "satelle-design-review") {
		t.Error("a surface:cli story must not carry the surface:ui design gate")
	}
	if !contains(skillsOf(cli.Skipped), "satelle-design-review") {
		t.Errorf("the design gate must be recorded as SKIPPED for surface:cli, not silently absent; got %v", cli.Skipped)
	}
}

// TestRouteSeparatesExitsFromSteps: park and cancel are exits, never steps. A
// route that listed them inline would claim the story passes through them.
func TestRouteSeparatesExitsFromSteps(t *testing.T) {
	r := Build(derived(t, nil), "wf", nil, nil)
	for _, s := range r.Steps {
		if s.Status == "blocked" || s.Status == "cancelled" {
			t.Fatalf("%q is an exit, not a step on the route", s.Status)
		}
	}
	exits := map[string]Exit{}
	for _, e := range r.Exits {
		exits[e.Status] = e
	}
	if !exits["blocked"].Park {
		t.Error("blocked must render as a park exit (from=* resume park)")
	}
	if exits["cancelled"].Park {
		t.Error("cancelled must render as terminal, not park — nothing resumes from it")
	}
}

// TestExitParkFollowsResumePark (sty_524f091d AC3): the promise is
// IsResumePark, not "has outbound edges". A cancel sink with an extra outbound
// edge is still not advertised as a park.
func TestExitParkFollowsResumePark(t *testing.T) {
	r := Build(derived(t, nil), "wf", nil, nil)
	out := r.Render("in_progress")
	if !strings.Contains(out, "blocked (park — resumes to origin)") {
		t.Errorf("default route must still advertise blocked as a resume park:\n%s", out)
	}
	if strings.Contains(out, "cancelled (park — resumes to origin)") {
		t.Errorf("cancelled must not be advertised as a resume park:\n%s", out)
	}
}

// TestRouteFitsTheLegibilityBudget (AC4 / the story's own test): the route is one
// line per step. If it overflows, the route has become too dynamic to read — the
// failure is the signal, not a nuisance.
func TestRouteFitsTheLegibilityBudget(t *testing.T) {
	out := Build(derived(t, nil), "satelle-project-workflow", nil, nil).Render("in_progress")
	steps := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "**") {
			steps++
		}
	}
	if steps != 6 {
		t.Errorf("rendered %d step lines; want one per step (6)", steps)
	}
	if lines := len(strings.Split(strings.TrimSpace(out), "\n")); lines > 12 {
		t.Errorf("route renders in %d lines; the budget is ~10 — the route is too dynamic to read", lines)
	}
	if !strings.Contains(out, "* 3. **in_progress**") {
		t.Errorf("the route must mark where the story IS; got:\n%s", out)
	}
	if strings.Contains(out, ".satelle/workflows") || strings.Contains(out, "```dot") {
		t.Error("the route must be readable without pointing at a workflow file")
	}
}

func stepAt(t *testing.T, r Route, status string) Step {
	t.Helper()
	for _, s := range r.Steps {
		if s.Status == status {
			return s
		}
	}
	t.Fatalf("no step %q on the route", status)
	return Step{}
}

func skillsOf(rs []Reviewer) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Skill)
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

// TestRouteNamesTheAdvisorsToConsult (sty_05a5e203 AC3): flat dispatch means
// entering a state fires nothing, so the route has to TELL the orchestrator who
// to consult. The park exit names its triage advisor and the terminal step names
// its retrospective one, both read from the substrate's `advise` declarations.
func TestRouteNamesTheAdvisorsToConsult(t *testing.T) {
	root := repoRoot(t)
	doneBody, err := os.ReadFile(filepath.Join(root, "internal", "wfdot", "testdata", "done.toml"))
	if err != nil {
		t.Fatalf("read done.toml: %v", err)
	}
	stepBody, err := os.ReadFile(filepath.Join(root, "internal", "wfdot", "testdata", "step.toml"))
	if err != nil {
		t.Fatalf("read step.toml: %v", err)
	}
	lists, err := wfdot.ParseDone(string(doneBody))
	if err != nil {
		t.Fatalf("ParseDone: %v", err)
	}
	cat, err := wfdot.ParseSteps(string(stepBody))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	var list wfdot.List
	for _, l := range lists {
		if l.Category == "feature" {
			list = l
		}
	}
	selected, serr := wfdot.SelectSteps(list, cat, nil)
	if serr != nil {
		t.Fatalf("SelectSteps: %v", serr)
	}
	advisors := AdvisorsFrom(list, selected)
	if len(advisors) < 2 {
		t.Fatalf("advisors = %+v; want at least the park and terminal advisors", advisors)
	}

	r := Build(derived(t, nil), "satelle-project-workflow", nil, advisors)

	var park *Exit
	for i, e := range r.Exits {
		if e.Status == "blocked" {
			park = &r.Exits[i]
		}
	}
	if park == nil || park.Advisor == nil || park.Advisor.Agent != "blocked-triage" {
		t.Fatalf("park exit advisor = %+v; want blocked-triage named on the route", park)
	}
	term := stepAt(t, r, "done")
	if term.Advisor == nil || term.Advisor.Agent != "retrospective" {
		t.Errorf("terminal advisor = %+v; want retrospective named on the route", term.Advisor)
	}

	out := r.Render("in_progress")
	for _, want := range []string{
		"advisor: retrospective under @skill:satelle-lessons",
		"advisor: blocked-triage under @skill:satelle-story-blocked-triage",
		"the orchestrator consults it and records the advice; nothing dispatches it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered route is missing %q:\n%s", want, out)
		}
	}
}
