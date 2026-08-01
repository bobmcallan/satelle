package wfequiv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// convertedWorkflows maps each RETIRED DOT workflow to the categories the
// derived route claims on its behalf (sty_9835070d). A workflow that governed
// several categories must reproduce for each of them, not just the first — the
// parent workflow governing both epic-parent and parent is the case that would
// otherwise pass while half-converted.
var convertedWorkflows = []struct {
	file       string
	categories []string
}{
	{"satelle-project-workflow.md", []string{"*"}},
	{"satelle-parent-workflow.md", []string{"epic-parent", "parent"}},
	{"satelle-substrate-workflow.md", []string{"substrate"}},
	{"satelle-task-workflow.md", []string{"execution", "task"}},
}

// loadRouteSource reads the live declaration of done and step catalogue. Unlike
// the authored side these are NOT frozen: they are what the repo runs on, so the
// checker compares the frozen past against the live present. A checkout without
// them skips rather than fails.
func loadRouteSource(t *testing.T) (done, step string) {
	t.Helper()
	dir := repoWorkflowDir()
	if dir == "" {
		t.Skip("no .satelle/workflows in this checkout")
	}
	d, err := os.ReadFile(filepath.Join(dir, "done.md"))
	if err != nil {
		t.Skip("no done.md — this repo has not converted")
	}
	s, err := os.ReadFile(filepath.Join(dir, "step.md"))
	if err != nil {
		t.Skip("no step.md — this repo has not converted")
	}
	return string(d), string(s)
}

// namedDivergences are the divergences the conversion accepts, each with the
// reason it is CORRECT rather than a conversion defect. AC3 requires a
// divergence to be named, never absorbed: anything not listed here fails.
//
// A key is "<file>/<category>"; a value is the substring every divergence line
// for that route must contain to be allowed. An empty map means the conversion
// reproduces the authored graph exactly.
var namedDivergences = map[string][]string{
	// The task workflow authored `cancelled [shape=Msquare]` — a terminal marked
	// as a SUCCESS terminal, because the DOT had no way to say "terminal, but not
	// a success". Under the route that reading is load-bearing: distToSuccess
	// treats an Msquare state as a destination, so `cancelled` would render as a
	// final STEP beside `done` instead of an off-route exit. The derived route
	// synthesises it as the cancel sink every other workflow already has
	// (agent=reviewer, unshaped), which is why agent/IsTerminalState/IsParkState
	// all differ on that one state.
	//
	// It is representational, not behavioural. Both consumers of these predicates
	// — wfdot.AdvanceOptions and verb.terminalOrParked — test
	// `IsTerminalState(x) || IsParkState(x)`, and the pair flips together: the
	// authored form is (true, false), the derived is (false, true), and the
	// disjunction is true either way. The project and substrate workflows already
	// authored cancelled this way, so the conversion makes task CONSISTENT with
	// them rather than changing what cancelling does.
	"satelle-task-workflow.md/execution": {"cancelled"},
	"satelle-task-workflow.md/task":      {"cancelled"},
}

// TestConvertedRoutesReproduceAuthoredGraphs is the safety net the story asks
// for: for every retired DOT workflow, and every category it governed, the
// derived route must reproduce its path, gates, scoped reviewers and executor
// rubrics across the tag matrix — or diverge only in a way named above.
func TestConvertedRoutesReproduceAuthoredGraphs(t *testing.T) {
	doneBody, stepBody := loadRouteSource(t)
	for _, wf := range convertedWorkflows {
		for _, category := range wf.categories {
			t.Run(wf.file+"/"+category, func(t *testing.T) {
				want := loadAuthored(t, wf.file)
				got, err := wfdot.ParseRoute(doneBody, stepBody, category, nil)
				if err != nil {
					t.Fatalf("derive %s for category %q: %v", wf.file, category, err)
				}
				if problems := wfdot.Validate(got); len(problems) != 0 {
					t.Fatalf("derived route for %q does not validate: %v", category, problems)
				}
				report := Diff(want, got)
				allowed := namedDivergences[wf.file+"/"+category]
				var unnamed []string
				for _, line := range allLines(report) {
					if !matchesAny(line, allowed) {
						unnamed = append(unnamed, line)
					}
				}
				if len(unnamed) > 0 {
					t.Errorf("%s → category %q diverges in ways this conversion does not name:\n  %s\n\nfull report:\n%s",
						wf.file, category, strings.Join(unnamed, "\n  "), report)
				}
			})
		}
	}
}

// TestConvertedRouteCheckIsNotVacuous proves the comparison above can fail: drop
// one gate from the derived side and the checker must notice. Without this, an
// all-green conversion report would be indistinguishable from a checker that
// compares nothing.
func TestConvertedRouteCheckIsNotVacuous(t *testing.T) {
	doneBody, stepBody := loadRouteSource(t)
	want := loadAuthored(t, "satelle-project-workflow.md")
	got, err := wfdot.ParseRoute(doneBody, stepBody, "*", nil)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	mutated := got
	mutated.Transitions = append([]wfdot.Transition(nil), got.Transitions...)
	for i := range mutated.Transitions {
		mutated.Transitions[i].Skills = nil
		mutated.Transitions[i].Skill = ""
	}
	if r := Diff(want, mutated); r.Empty() {
		t.Fatal("stripping every gate from the derived route produced no divergence — the check is vacuous")
	}
}

// TestGateCategoryScopeIsEnforced covers the grammar addition this conversion
// needed (sty_9835070d): the step catalogue is shared, so without a category
// scope the deployment gates would fire on the substrate and task routes —
// workflows that deliberately carry no release to verify.
func TestGateCategoryScopeIsEnforced(t *testing.T) {
	doneBody, stepBody := loadRouteSource(t)
	deployment := []string{
		"satelle-ci-published-check",
		"satelle-dogfood-check",
		"satelle-changelog-entry-check",
		"satelle-integration-check",
	}
	for _, category := range []string{"substrate", "execution", "task", "epic-parent"} {
		spec, err := wfdot.ParseRoute(doneBody, stepBody, category, nil)
		if err != nil {
			t.Fatalf("derive %q: %v", category, err)
		}
		for _, st := range spec.States {
			for _, d := range deployment {
				if st.Skill == d {
					t.Errorf("category %q carries deployment gate %s — it has no release to verify", category, d)
				}
			}
		}
	}
	// …and the wildcard route, which DOES release, still carries all of them.
	spec, err := wfdot.ParseRoute(doneBody, stepBody, "*", nil)
	if err != nil {
		t.Fatalf("derive wildcard: %v", err)
	}
	have := map[string]bool{}
	for _, st := range spec.States {
		have[st.Skill] = true
	}
	for _, d := range deployment {
		if !have[d] {
			t.Errorf("the wildcard route lost deployment gate %s", d)
		}
	}
}

// TestWildcardSectionGovernsUnlistedCategory: the project workflow was
// applies_to ["*"], and this repo carries 26 distinct live categories.
// Enumerating them would brick the next new one, so `## *` governs anything with
// no section of its own — the same precedence applies_to already had.
func TestWildcardSectionGovernsUnlistedCategory(t *testing.T) {
	doneBody, stepBody := loadRouteSource(t)
	wild, err := wfdot.ParseRoute(doneBody, stepBody, "*", nil)
	if err != nil {
		t.Fatalf("derive wildcard: %v", err)
	}
	for _, category := range []string{"feature", "fix", "cli", "tooling", "a-category-invented-tomorrow"} {
		got, err := wfdot.ParseRoute(doneBody, stepBody, category, nil)
		if err != nil {
			t.Fatalf("category %q must fall back to the wildcard section: %v", category, err)
		}
		if r := Diff(wild, got); !r.Empty() {
			t.Errorf("category %q resolved to something other than the wildcard route:\n%s", category, r)
		}
	}
	// A category WITH its own section keeps it — fallback must not shadow.
	sub, err := wfdot.ParseRoute(doneBody, stepBody, "substrate", nil)
	if err != nil {
		t.Fatalf("derive substrate: %v", err)
	}
	if r := Diff(wild, sub); r.Empty() {
		t.Error("substrate resolved to the wildcard route — its own section was ignored")
	}
}

func allLines(r Report) []string {
	var out []string
	out = append(out, r.Path...)
	out = append(out, r.Gates...)
	out = append(out, r.Scoped...)
	out = append(out, r.Executor...)
	return out
}

func matchesAny(line string, allowed []string) bool {
	for _, a := range allowed {
		if strings.Contains(line, a) {
			return true
		}
	}
	return false
}
