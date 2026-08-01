package wfequiv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// The EMBEDDED default substrate converted from four DOT graphs to one derived
// route (sty_3795e7f6). This is the same safety net the repo's own conversion
// got (converted_test.go), pointed at the shipped defaults: the bodies of the
// retired graphs are frozen under testdata/embedded, and the live embedded
// route must reproduce each one — or diverge only where the divergence is named.
var convertedEmbedded = []struct {
	file       string
	categories []string
}{
	{"satelle-baseline-workflow.md", []string{"*"}},
	{"satelle-parent-workflow.md", []string{"epic-parent", "parent"}},
	{"satelle-task-workflow.md", []string{"execution", "task"}},
}

// embeddedNamedDivergences carries the same reading converted_test.go records
// for the repo's own task lifecycle: the authored task graph wrote
// `cancelled [shape=Msquare]`, a terminal marked as a SUCCESS terminal because
// the DOT had no way to say "terminal, but not a success". The derived route
// synthesises it as the cancel sink every other lifecycle already has, so
// agent/IsTerminalState/IsParkState differ on that one state and on nothing
// else. It is representational: both consumers test
// `IsTerminalState(x) || IsParkState(x)`, and the pair flips together.
//
// The baseline's own divergence is the mirror image. It authored
// `done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]`
// — the node carried its close reviewer, and the graph had no other way to say
// so. IsParkState is `agent == "reviewer" && shape != "Mdiamond"`, so the
// SUCCESS terminal reported itself as a PARK state. Under the route the close
// gate belongs to the edge into `done` (it is the step's entry reviewer, and the
// derived edge carries the same three rubrics), so the node needs no agent and
// `done` is terminal-and-not-parked. Nothing reads IsParkState of a terminal
// state — park-resume applies to where a story currently sits, and a story at
// done cannot move (satelle-done-is-last) — so this drops a wart rather than
// changing behaviour, and reproducing it would carry the wart into the route.
var embeddedNamedDivergences = map[string][]string{
	"satelle-task-workflow.md/execution": {"cancelled"},
	"satelle-task-workflow.md/task":      {"cancelled"},
	"satelle-baseline-workflow.md/*":     {`state "done" agent`, `state "done" IsParkState`},
}

// embeddedRouteSource returns the two halves the binary ships.
func embeddedRouteSource(t *testing.T) (done, step string) {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
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
		t.Fatal("the binary must ship both halves of the default route (workflows/done.md + step.md)")
	}
	return done, step
}

func loadRetiredEmbedded(t *testing.T, name string) wfdot.Spec {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "embedded", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	spec, ok := wfdot.Parse(string(body))
	if !ok {
		t.Fatalf("%s: no parseable dot block", name)
	}
	return spec
}

// TestEmbeddedRouteReproducesRetiredGraphs is the conversion proof for the
// SHIPPED defaults: every retired embedded graph, for every category it
// governed, is reproduced by the derived route.
func TestEmbeddedRouteReproducesRetiredGraphs(t *testing.T) {
	doneBody, stepBody := embeddedRouteSource(t)
	for _, wf := range convertedEmbedded {
		for _, category := range wf.categories {
			t.Run(wf.file+"/"+category, func(t *testing.T) {
				want := loadRetiredEmbedded(t, wf.file)
				got, err := wfdot.ParseRoute(doneBody, stepBody, category, nil)
				if err != nil {
					t.Fatalf("derive %s for category %q: %v", wf.file, category, err)
				}
				if problems := wfdot.Validate(got); len(problems) != 0 {
					t.Fatalf("derived route for %q does not validate: %v", category, problems)
				}
				report := Diff(want, got)
				allowed := embeddedNamedDivergences[wf.file+"/"+category]
				var unnamed []string
				for _, line := range allLines(report) {
					if !matchesAny(line, allowed) {
						unnamed = append(unnamed, line)
					}
				}
				if len(unnamed) != 0 {
					t.Errorf("%s → category %q diverges from the retired graph in ways nothing names:\n  %s\n\nfull report:\n%s",
						wf.file, category, strings.Join(unnamed, "\n  "), report)
				}
			})
		}
	}
}

// TestEmbeddedSubstrateLaneRetired states the one deliberate omission (AC3): the
// shipped route has no `substrate` section. The substrate lane exists to let a
// markdown-only change SKIP a heavier lane, and the default has exactly one
// working lane — so a second one would only offer a way around the default
// gates. satelle-substrate-only-check still ships, for a repo that declares the
// section itself.
func TestEmbeddedSubstrateLaneRetired(t *testing.T) {
	doneBody, _ := embeddedRouteSource(t)
	lists, err := wfdot.ParseDone(doneBody)
	if err != nil {
		t.Fatalf("parse embedded done.md: %v", err)
	}
	for _, l := range lists {
		if l.Category == "substrate" {
			t.Fatal("the shipped route declares a `substrate` section — either the decision changed " +
				"and this test should record the new one, or the section slipped in unstated")
		}
	}
	var hasCheck bool
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-substrate-only-check" {
			hasCheck = true
		}
	}
	if !hasCheck {
		t.Error("satelle-substrate-only-check must still ship: a repo that declares its own substrate " +
			"section resolves the gate from the embedded set")
	}
}
