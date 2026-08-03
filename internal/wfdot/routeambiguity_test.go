package wfdot

import (
	"strings"
	"testing"
)

// Construction must fail closed on an ambiguous selection (sty_ee0f4ae6). Before
// this, topoSortSteps keyed its maps by step NAME and resolved a duplicate
// last-wins, so one step vanished from the route carrying its reviewers with it —
// the "route that quietly loses a gate" the constructor exists to prevent.

// AC1: two selected steps sharing a stage name. This is one done.toml edit away
// — a category listing both `coded` and `authored` selects two steps whose
// `status` is `in_progress`.
func TestDuplicateSelectedStepNameIsError(t *testing.T) {
	step := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
skills = ["code"]
reviewers = ["gate-a"]
requires = ["raised"]

[authored]
status = "in_progress"
agent = "executor"
skills = ["substrate"]
reviewers = ["gate-b"]
requires = ["raised"]
`
	spec, err := ParseRoute(`[feature]
obligations = ["raised", "coded", "authored"]
`, step, "feature", nil)
	if err == nil {
		t.Fatal("selecting two steps named in_progress must fail construction")
	}
	// The stage name alone cannot identify WHICH two sections to fix, so the
	// message must carry both obligations.
	for _, want := range []string{"in_progress", `"coded"`, `"authored"`, "feature"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must locate both colliding sections; missing %s in: %v", want, err)
		}
	}
	if len(spec.States) != 0 || len(spec.Transitions) != 0 {
		t.Fatalf("a failed construction must produce no partial Spec, got %d states / %d transitions",
			len(spec.States), len(spec.Transitions))
	}
}

// AC2, at the AUTHORED layer: a catalogue cannot even express two steps
// discharging one obligation, because the obligation is the table KEY. What used
// to need a construction-time refusal is now a duplicate-key error from the TOML
// decoder (sty_81bb0dde) — the representation makes the defect unrepresentable,
// which is stronger than catching it. It must still fail CLOSED, naming the
// obligation and yielding no partial Spec.
func TestDuplicateProvidesInCatalogueIsError(t *testing.T) {
	step := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
reviewers = ["gate-a"]
requires = ["raised"]

[coded]
status = "rework"
agent = "executor"
reviewers = ["gate-b"]
requires = ["raised"]
`
	spec, err := ParseRoute(`[feature]
obligations = ["raised", "coded"]
`, step, "feature", nil)
	if err == nil {
		t.Fatal("two steps providing one obligation must fail construction")
	}
	for _, want := range []string{"step.toml", "coded"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must name the file and the obligation; missing %s in: %v", want, err)
		}
	}
	if len(spec.States) != 0 || len(spec.Transitions) != 0 {
		t.Fatal("a failed construction must produce no partial Spec")
	}
}

// AC2, at the CONSTRUCTOR layer: Catalogue is public and BuildRoute is reachable
// without the parser, so the last-wins guard the parser now pre-empts must stay
// proven on its own. Both BuildRoute's `provided` set and topoSortSteps'
// `provider` map resolved a duplicate last-wins — a step vanished from the route
// carrying its reviewers with it.
func TestDuplicateSelectedProvidesIsError(t *testing.T) {
	cat := Catalogue{Steps: []Step{
		{Name: "backlog", Provides: "raised", Start: true},
		{Name: "in_progress", Provides: "coded", Agent: "executor",
			Reviewers: []string{"gate-a"}, Requires: []string{"raised"}},
		{Name: "rework", Provides: "coded", Agent: "executor",
			Reviewers: []string{"gate-b"}, Requires: []string{"raised"}},
	}}
	l := List{Category: "feature", Obligations: []string{"raised", "coded"}}

	spec, err := BuildRoute(l, cat, nil)
	if err == nil {
		t.Fatal("two steps providing one obligation must fail construction")
	}
	for _, want := range []string{`"coded"`, "in_progress", "rework", "feature"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must name the obligation and both steps; missing %s in: %v", want, err)
		}
	}
	if len(spec.States) != 0 || len(spec.Transitions) != 0 {
		t.Fatal("a failed construction must produce no partial Spec")
	}
}

// AC3: the catalogue legitimately holds several steps per stage name — one
// `## done` per route family is the normal shape. Only CO-SELECTION is the
// defect, so a catalogue-wide duplicate that no category selects together must
// still build. A catalogue-wide check would have broken every route on day one.
func TestCatalogueDuplicateNamesStillBuild(t *testing.T) {
	step := `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[authored]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[substrate-verified]
status = "done"
terminal = true
requires = ["authored"]
`
	done := `[feature]
obligations = ["raised", "coded", "closed"]

[substrate]
obligations = ["raised", "authored", "substrate-verified"]
`

	for _, category := range []string{"feature", "substrate"} {
		spec, err := ParseRoute(done, step, category, nil)
		if err != nil {
			t.Fatalf("category %q must still build from a catalogue with duplicate stage names: %v", category, err)
		}
		if len(spec.States) == 0 {
			t.Fatalf("category %q built an empty route", category)
		}
		if problems := Validate(spec); len(problems) != 0 {
			t.Fatalf("category %q must validate clean, got %v", category, problems)
		}
	}
}

// AC3, against the real fixture pair: every category the fixture declares must
// still resolve, so the new refusal cannot regress a route that works today.
func TestEveryFixtureCategoryStillBuilds(t *testing.T) {
	done, step := fixtures(t)
	lists, err := ParseDone(done)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) == 0 {
		t.Fatal("fixture declares no categories")
	}
	for _, l := range lists {
		if _, err := ParseRoute(done, step, l.Category, nil); err != nil {
			t.Fatalf("fixture category %q no longer builds: %v", l.Category, err)
		}
	}
}

// AC4: Validate reports a duplicate state name. Defence in depth — BuildRoute
// should now be incapable of emitting one, but Spec is public and every
// downstream predicate treats a name as a unique key.
func TestValidateReportsDuplicateStates(t *testing.T) {
	spec := Spec{
		States: []State{
			{Name: "backlog"},
			{Name: "plan"},
			{Name: "plan"},
			{Name: "done"},
		},
		Transitions: []Transition{
			{From: "backlog", To: "plan"},
			{From: "plan", To: "done"},
		},
	}
	problems := Validate(spec)
	var hit int
	for _, p := range problems {
		if strings.Contains(p, "plan") && strings.Contains(p, "more than once") {
			hit++
		}
	}
	if hit == 0 {
		t.Fatalf("Validate must report the duplicate state, got %v", problems)
	}
	// Once per name regardless of multiplicity — N-1 near-identical problems is
	// noise, not information.
	if hit > 1 {
		t.Fatalf("the duplicate must be reported once, got %d times: %v", hit, problems)
	}
}

func TestValidateAcceptsUniqueStates(t *testing.T) {
	spec := Spec{
		States:      []State{{Name: "backlog"}, {Name: "done"}},
		Transitions: []Transition{{From: "backlog", To: "done"}},
	}
	for _, p := range Validate(spec) {
		if strings.Contains(p, "more than once") {
			t.Fatalf("unique states must not trip the duplicate check: %v", p)
		}
	}
}
