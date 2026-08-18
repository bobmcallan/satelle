package wfdot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtures(t *testing.T) (done, step string) {
	t.Helper()
	d, err := os.ReadFile(filepath.Join("testdata", "done.toml"))
	if err != nil {
		t.Fatalf("read done.toml: %v", err)
	}
	s, err := os.ReadFile(filepath.Join("testdata", "step.toml"))
	if err != nil {
		t.Fatalf("read step.toml: %v", err)
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
	if !spec.IsResumePark("blocked") {
		t.Error("blocked is the from=* resume park")
	}
	if spec.IsResumePark("cancelled") {
		t.Error("cancelled is not a resume park")
	}
}

// TestSynthesisedTopologyValidates is AC2: the author declares no cancel, park or
// backward edge, and all three appear — carrying their role state's own gate.
func TestSynthesisedTopologyValidates(t *testing.T) {
	done, step := fixtures(t)
	for _, forbidden := range []string{"[cancelled]", "[blocked]"} {
		if strings.Contains(step, forbidden) {
			t.Fatalf("step.toml declares %q — topology must be synthesised, not authored", forbidden)
		}
	}
	if strings.Contains(done, "->") {
		t.Fatal("done.toml names an edge — topology must be synthesised, not authored")
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
	// The park advisor is DECLARED (`park.advisor` in done.toml) but is
	// deliberately absent from the Spec: flat dispatch means entry fires nothing,
	// so who to consult is an instruction to the orchestrator carried on the
	// route, not topology (sty_05a5e203).
	if agent, _ := spec.StateAgent("blocked"); agent != "reviewer" {
		t.Errorf("park role agent = %q, want reviewer", agent)
	}
}

// TestRouteOrderFromPrerequisites is AC3: declaration order is scrambled, and the
// route still comes out in prerequisite order.
func TestRouteOrderFromPrerequisites(t *testing.T) {
	step := `[released]
status = "release"
agent = "executor"
requires = ["coded"]

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]
`
	done := `[feature]
obligations = ["raised", "coded", "released"]
`
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
	step := `[x]
status = "a"
agent = "executor"
requires = ["y"]

[y]
status = "b"
agent = "executor"
requires = ["x"]
`
	_, err := ParseRoute(`[feature]
obligations = ["x", "y"]
`, step, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a prerequisite cycle must be an error naming it, got %v", err)
	}
}

func TestRouteOrphanObligationNamesIt(t *testing.T) {
	step := `[x]
status = "a"
agent = "executor"
`
	_, err := ParseRoute(`[feature]
obligations = ["x", "deployed"]
`, step, "feature", nil)
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
	done := `[feature]
obligations = ["raised", "coded"]

[[feature.tag_obligation]]
tag = "surface:ui"
obligation = "styled"
`
	step := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[styled]
status = "style"
agent = "executor"
requires = ["coded"]
`

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

// A key no wire struct claims is an ERROR, never a silent drop: a typo'd
// `reviewrs =` that parsed as "no reviewers" would lose a gate, and a route that
// quietly loses a gate is the one failure this representation must not have. The
// message names the FILE and the offending key path, because that is what an
// author needs to fix it.
func TestParseErrorsNameTheKey(t *testing.T) {
	const doneOK = "[feature]\nobligations = [\"x\"]\n"
	const stepOK = "[x]\nstatus = \"in_progress\"\n"
	for _, tc := range []struct {
		name, done, step string
		want             []string
	}{
		{
			name: "unknown done key",
			done: "[feature]\nobligations = [\"x\"]\nwibble = \"y\"\n", step: stepOK,
			want: []string{"done.toml", "unknown key", "feature.wibble"},
		},
		{
			name: "unknown step key",
			done: doneOK, step: "[x]\nstatus = \"in_progress\"\nreviewrs = [\"gate-a\"]\n",
			want: []string{"step.toml", "unknown key", "x.reviewrs"},
		},
		{
			name: "non-numeric parallel",
			done: doneOK, step: "[x]\nstatus = \"in_progress\"\nparallel = \"lots\"\n",
			want: []string{"step.toml", "parallel"},
		},
		{
			// A step with no status has no stage name for an item to hold, so the
			// route has nowhere to put it.
			name: "step without a status",
			done: doneOK, step: "[x]\nagent = \"executor\"\n",
			want: []string{"step.toml", `step "x"`, "no status"},
		},
		{
			// Malformed TOML is reported by the real parser, with a line number —
			// the whole point of retiring the hand-rolled reader.
			name: "malformed toml",
			done: "[feature\nobligations = [\"x\"]\n", step: stepOK,
			want: []string{"done.toml", "line 2", "end table name"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRoute(tc.done, tc.step, "feature", nil)
			if err == nil {
				t.Fatalf("want an error, got none")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}

func TestMultiSkillStepIsError(t *testing.T) {
	step := `[raised]
status = "backlog"
start = true

[x]
status = "a"
agent = "executor"
skills = ["one", "two"]
requires = ["raised"]
`
	_, err := ParseRoute(`[feature]
obligations = ["raised", "x"]
`, step, "feature", nil)
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
