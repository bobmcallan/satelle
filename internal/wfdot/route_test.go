package wfdot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtures(t *testing.T) (done, step string) {
	t.Helper()
	d, err := os.ReadFile(filepath.Join("testdata", "done.md"))
	if err != nil {
		t.Fatalf("read done.md: %v", err)
	}
	s, err := os.ReadFile(filepath.Join("testdata", "step.md"))
	if err != nil {
		t.Fatalf("read step.md: %v", err)
	}
	return string(d), string(s)
}

func buildFixture(t *testing.T, tags []string) Spec {
	t.Helper()
	done, step := fixtures(t)
	spec, err := ParseRoute(done, step, "feature", tags)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return spec
}

// TestParseRouteBuildsValidSpec is AC1: two markdown files in, a valid Spec out,
// with no DOT text anywhere in the path.
func TestParseRouteBuildsValidSpec(t *testing.T) {
	spec := buildFixture(t, nil)
	if problems := Validate(spec); len(problems) != 0 {
		t.Fatalf("derived spec does not validate: %v", problems)
	}
	if got := spec.Start(); got != "backlog" {
		t.Errorf("Start() = %q, want backlog", got)
	}
	for _, tc := range []struct {
		from, to string
	}{
		{"backlog", "plan"}, {"plan", "in_progress"}, {"in_progress", "integration"},
		{"integration", "release"}, {"release", "done"},
	} {
		if !spec.HasEdge(tc.from, tc.to) {
			t.Errorf("spine edge %s->%s missing", tc.from, tc.to)
		}
	}
	wantPerforming := []string{"plan", "in_progress", "integration", "release"}
	if got := spec.PerformingStates(); !sameStrings(got, wantPerforming) {
		t.Errorf("PerformingStates() = %v, want %v", got, wantPerforming)
	}
	wantEdit := []string{"in_progress", "integration", "release"}
	if got := spec.EditCapableStates(); !sameStrings(got, wantEdit) {
		t.Errorf("EditCapableStates() = %v, want %v", got, wantEdit)
	}
	if !spec.IsTerminalState("done") {
		t.Error("done must be terminal")
	}
	if !spec.IsParkState("blocked") || !spec.IsParkState("cancelled") {
		t.Error("blocked and cancelled must be park/role states")
	}
}

// TestSynthesisedTopologyValidates is AC2: the author declares no cancel, park or
// backward edge, and all three appear — carrying their role state's own gate.
func TestSynthesisedTopologyValidates(t *testing.T) {
	done, step := fixtures(t)
	for _, forbidden := range []string{"## cancelled", "## blocked"} {
		if strings.Contains(step, forbidden) {
			t.Fatalf("step.md declares %q — topology must be synthesised, not authored", forbidden)
		}
	}
	if strings.Contains(done, "->") {
		t.Fatal("done.md names an edge — topology must be synthesised, not authored")
	}

	spec := buildFixture(t, nil)
	if problems := Validate(spec); len(problems) != 0 {
		t.Fatalf("synthesised spec does not validate: %v", problems)
	}
	for _, want := range []string{
		"backlog->cancelled", "plan->cancelled", "in_progress->cancelled",
		"integration->cancelled", "release->cancelled", "blocked->cancelled",
		"plan->blocked", "in_progress->blocked", "integration->blocked", "release->blocked",
		"integration->in_progress", "release->in_progress",
	} {
		from, to, _ := strings.Cut(want, "->")
		if !spec.HasEdge(from, to) {
			t.Errorf("synthesised edge %s missing", want)
		}
	}
	// Nothing has begun at the start state, and a terminal cannot be abandoned.
	if spec.HasEdge("backlog", "blocked") {
		t.Error("start state must not gain a park edge")
	}
	if spec.HasEdge("done", "cancelled") || spec.HasEdge("done", "blocked") {
		t.Error("terminal state must not gain cancel or park edges")
	}
	// Role-state gates ride the synthesised edges, or the reviewers are lost.
	idx := map[string]Transition{}
	for _, tr := range spec.Transitions {
		idx[tr.From+"->"+tr.To] = tr
	}
	if got := idx["in_progress->cancelled"].Skill; got != "satelle-story-cancel-review" {
		t.Errorf("cancel edge gate = %q, want satelle-story-cancel-review", got)
	}
	if got := idx["in_progress->blocked"].Skill; got != "satelle-story-blocked-review" {
		t.Errorf("park edge gate = %q, want satelle-story-blocked-review", got)
	}
	if agent, _ := spec.StateOnEnterAgent("blocked"); agent != "blocked-triage" {
		t.Errorf("park advisor = %q, want blocked-triage", agent)
	}
}

// TestRouteOrderFromPrerequisites is AC3: declaration order is scrambled, and the
// route still comes out in prerequisite order.
func TestRouteOrderFromPrerequisites(t *testing.T) {
	step := `
## release
agent: executor
provides: released
requires: coded
## backlog
start: true
provides: raised
## in_progress
agent: executor
provides: coded
requires: raised
`
	done := "## feature\n- raised\n- coded\n- released\n"
	spec, err := ParseRoute(done, step, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	var names []string
	for _, st := range spec.States {
		names = append(names, st.Name)
	}
	if want := []string{"backlog", "in_progress", "release"}; !sameStrings(names, want) {
		t.Errorf("derived order = %v, want %v", names, want)
	}
}

func TestRouteCycleIsError(t *testing.T) {
	step := "## a\nagent: executor\nprovides: x\nrequires: y\n## b\nagent: executor\nprovides: y\nrequires: x\n"
	_, err := ParseRoute("## feature\n- x\n- y\n", step, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a prerequisite cycle must be an error naming it, got %v", err)
	}
}

func TestRouteOrphanObligationNamesIt(t *testing.T) {
	step := "## a\nagent: executor\nprovides: x\n"
	_, err := ParseRoute("## feature\n- x\n- deployed\n", step, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "deployed") {
		t.Fatalf("an obligation with no discharging step must be NAMED, got %v", err)
	}
}

func TestRouteUnknownCategoryIsError(t *testing.T) {
	done, step := fixtures(t)
	_, err := ParseRoute(done, step, "nonesuch", nil)
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("an unknown category must be an error naming it, got %v", err)
	}
}

// TestStepReviewersAreConcurrentAndComplete is AC4: every declared reviewer
// survives into the transition, and two or more run concurrently by default.
func TestStepReviewersAreConcurrentAndComplete(t *testing.T) {
	spec := buildFixture(t, nil)
	idx := map[string]Transition{}
	for _, tr := range spec.Transitions {
		idx[tr.From+"->"+tr.To] = tr
	}

	plan := idx["plan->in_progress"]
	want := []string{
		"satelle-story-plan-review",
		"satelle-story-architecture-review",
		"satelle-story-integration-coverage-review",
	}
	if !sameStrings(plan.Skills, want) {
		t.Errorf("plan gates = %v, want %v (order preserved, none dropped)", plan.Skills, want)
	}
	if plan.Skill != want[0] {
		t.Errorf("legacy Skill = %q, want %q", plan.Skill, want[0])
	}
	if plan.Parallel != DefaultParallelCap {
		t.Errorf("three unset reviewers must default to concurrent, got parallel=%d", plan.Parallel)
	}
	if single := idx["integration->release"]; single.Parallel != 0 {
		t.Errorf("a single reviewer needs no concurrency, got parallel=%d", single.Parallel)
	}
	// An authored value wins, including an explicit 0 on a multi-reviewer step.
	if authored := idx["in_progress->integration"]; authored.Parallel != 0 {
		t.Errorf("authored parallel: 0 must win, got %d", authored.Parallel)
	}
	if got := len(idx["in_progress->integration"].Skills); got != 3 {
		t.Errorf("authored parallel must not drop reviewers, got %d", got)
	}
}

// TestGatesSurviveScoping asserts the always-on gates reach the states they name,
// and that a surface-scoped gate stays scoped.
func TestGatesSurviveScoping(t *testing.T) {
	spec := buildFixture(t, nil)
	for _, tc := range []struct {
		state string
		want  []string
	}{
		{"integration", []string{"satelle-build-unit-check", "satelle-format-vet-check"}},
		{"release", []string{"satelle-integration-check"}},
		{"done", []string{
			"satelle-changelog-entry-check", "satelle-ci-published-check",
			"satelle-dogfood-check", "satelle-estimate-actual-review",
		}},
	} {
		enqueued, _ := spec.ScopedReviewersSplit(tc.state, nil)
		var got []string
		for _, r := range enqueued {
			got = append(got, r.Skill)
		}
		if !sameStrings(got, tc.want) {
			t.Errorf("gates on %s = %v, want %v", tc.state, got, tc.want)
		}
	}
	// surface:ui only.
	ui, _ := spec.ScopedReviewersSplit("integration", []string{"surface:ui"})
	var found bool
	for _, r := range ui {
		if r.Skill == "satelle-design-review" {
			found = true
		}
	}
	if !found {
		t.Error("design gate must fire for a surface:ui story")
	}
	_, skipped := spec.ScopedReviewersSplit("integration", []string{"surface:cli"})
	if len(skipped) == 0 {
		t.Error("design gate must be declared-and-skipped for a surface:cli story, not absent")
	}
	if agent, _, mandatory := spec.StepSummaryBinding(); agent != "reviewer-summary" || !mandatory {
		t.Errorf("step summary binding = %q mandatory=%t, want reviewer-summary mandatory", agent, mandatory)
	}
}

// TestTagObligationsAppend covers the "tags append obligations" rule end to end.
func TestTagObligationsAppend(t *testing.T) {
	done := "## feature\n- raised\n- coded\n+ surface:ui styled\n"
	step := "## backlog\nstart: true\nprovides: raised\n" +
		"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n" +
		"## style\nagent: executor\nprovides: styled\nrequires: coded\n"

	plain, err := ParseRoute(done, step, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute (untagged): %v", err)
	}
	if plain.HasEdge("in_progress", "style") {
		t.Error("untagged story must not get the tag-appended step")
	}
	tagged, err := ParseRoute(done, step, "feature", []string{"surface:ui"})
	if err != nil {
		t.Fatalf("ParseRoute (tagged): %v", err)
	}
	if !tagged.HasEdge("in_progress", "style") {
		t.Error("surface:ui story must get the tag-appended step")
	}
}

func TestParseErrorsNameTheLine(t *testing.T) {
	for _, tc := range []struct {
		name, done, step, want string
	}{
		{"unknown done key", "## feature\nwibble: x\n", "", "unknown key"},
		{"bad tag line", "## feature\n+ lonely\n", "", "expected"},
		{"unknown step key", "## feature\n- x\n", "## a\nwibble: y\n", "unknown step key"},
		{"non-numeric parallel", "## feature\n- x\n", "## a\nparallel: lots\n", "must be a number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRoute(tc.done, tc.step, "feature", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestMultiSkillStepIsError(t *testing.T) {
	step := "## backlog\nstart: true\nprovides: raised\n## a\nagent: executor\nskills: one, two\nprovides: x\nrequires: raised\n"
	_, err := ParseRoute("## feature\n- raised\n- x\n", step, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "one rubric") {
		t.Fatalf("a multi-skill spine step must be an error, got %v", err)
	}
}

func sameStrings(a, b []string) bool {
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
