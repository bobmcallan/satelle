package wfequiv

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata goldens")

// projectRouteList is satelle-project-workflow.md transcribed by hand into the
// obligation shape the epic proposes — the definition of done for a code change,
// plus the step catalogue that discharges it. This transcription IS the paper
// check: if the shape cannot carry the workflow, the divergence report says so
// and the epic stops (story sty_c6184eaa).
//
// Order is deliberately NOT declared. Every step names the obligation it provides
// and the obligations it requires, and Build topologically sorts them — the
// epic's rule that an orchestrator chooses the set, never the sequence.
func projectRouteList() List {
	return List{
		Category: "feature",
		Steps: []Step{
			{
				Name: "backlog", Start: true, Provides: "raised",
			},
			{
				Name: "plan", Agent: "planner", Skills: []string{"plan"},
				Provides: "planned", Requires: []string{"raised"},
				Reviewers: []string{"satelle-story-intent-review"}, ReviewerAgent: "reviewer",
			},
			{
				Name: "in_progress", Agent: "executor", Skills: []string{"code"},
				Provides: "coded", Requires: []string{"planned"},
				Reviewers: []string{
					"satelle-story-plan-review",
					"satelle-story-architecture-review",
					"satelle-story-integration-coverage-review",
				},
				ReviewerAgent: "reviewer", Parallel: 4,
			},
			{
				Name: "integration", Agent: "executor", Skills: []string{"integrate"},
				Provides: "integrated", Requires: []string{"coded"},
				Reviewers: []string{
					"satelle-code-ac-review",
					"satelle-story-scope-review",
					"satelle-workflow-change-review",
				},
				ReviewerAgent: "reviewer",
			},
			{
				Name: "release", Agent: "executor", Skills: []string{"release"},
				Provides: "released", Requires: []string{"integrated"},
				Reviewers: []string{"satelle-integration-review"}, ReviewerAgent: "reviewer",
			},
			{
				Name: "done", Terminal: true, Requires: []string{"released"},
				Reviewers: []string{"satelle-story-release-review"}, ReviewerAgent: "reviewer",
			},
		},
		Gates: []Gate{
			{Skill: "satelle-step-summary", Agent: "reviewer-summary", Mandatory: true},
			{Skill: "satelle-estimate-actual-review", On: []string{"in_progress", "done"}},
			{Skill: "satelle-format-vet-check", On: []string{"integration"}},
			{Skill: "satelle-build-unit-check", On: []string{"integration"}},
			{Skill: "satelle-integration-check", On: []string{"release"}},
			{Skill: "satelle-ci-published-check", On: []string{"done"}},
			{Skill: "satelle-dogfood-check", On: []string{"done"}},
			{Skill: "satelle-changelog-entry-check", On: []string{"done"}},
			{Skill: "satelle-design-review", Agent: "reviewer",
				On: []string{"integration"}, AppliesTo: []string{"surface:ui"}},
		},
		Cancel: "cancelled", CancelGate: "satelle-story-cancel-review",
		Park: "blocked", ParkGate: "satelle-story-blocked-review",
		Recover: "in_progress", RecoverFrom: []string{"integration", "release"},
	}
}

// TestProjectRouteEquivalence is the story's deliverable. It does NOT assert the
// report is empty: a non-empty divergence is a finding, not a failure. Asserting
// emptiness here would either force the obligation list to be fudged until it
// agreed, or turn an honest negative result into a red build — either way it
// would launder the epic's go/no-go signal.
func TestProjectRouteEquivalence(t *testing.T) {
	authored := loadAuthored(t, "satelle-project-workflow.md")
	derived, unexpressed := Build(projectRouteList())
	report := Diff(authored, derived)

	var b strings.Builder
	b.WriteString("# satelle-project-workflow — authored DOT vs derived route\n\n")
	b.WriteString("## Divergence\n\n")
	b.WriteString(report.String())
	b.WriteString("\n## Unexpressed by the obligation shape\n\n")
	if len(unexpressed) == 0 {
		b.WriteString("(none reported by Build)\n")
	}
	for _, u := range unexpressed {
		b.WriteString("  - " + u + "\n")
	}
	got := b.String()

	path := filepath.Join("testdata", "project-route.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated (%d divergences, %d unexpressed)", report.Count(), len(unexpressed))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if string(want) != got {
		t.Errorf("project-route golden drifted.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestProjectRouteSpineReproduces asserts the ONE thing the epic's go/no-go turns
// on: the derived route must carry the same spine — the same performing states,
// the same edit-capable states, and the same reviewer gates admitting each. Fog
// around on_enter dispatch and other authored attributes is recorded in the
// golden; the spine is not negotiable.
func TestProjectRouteSpineReproduces(t *testing.T) {
	authored := loadAuthored(t, "satelle-project-workflow.md")
	derived, _ := Build(projectRouteList())

	if !equalStrings(authored.PerformingStates(), derived.PerformingStates()) {
		t.Errorf("performing states diverge: want %v, got %v",
			authored.PerformingStates(), derived.PerformingStates())
	}
	if !equalStrings(authored.EditCapableStates(), derived.EditCapableStates()) {
		t.Errorf("edit-capable states diverge: want %v, got %v",
			authored.EditCapableStates(), derived.EditCapableStates())
	}
	if !equalStrings(authored.NonTerminalEngagingStates(), derived.NonTerminalEngagingStates()) {
		t.Errorf("engaging states diverge: want %v, got %v",
			authored.NonTerminalEngagingStates(), derived.NonTerminalEngagingStates())
	}
	wi, di := transitionIndex(authored), transitionIndex(derived)
	for _, edge := range []string{
		"backlog->plan", "plan->in_progress", "in_progress->integration",
		"integration->release", "release->done",
	} {
		w, ok := wi[edge]
		if !ok {
			t.Fatalf("authored workflow lost spine edge %s", edge)
		}
		d, ok := di[edge]
		if !ok {
			t.Errorf("derived route missing spine edge %s", edge)
			continue
		}
		if !equalStrings(gateSkills(w), gateSkills(d)) {
			t.Errorf("edge %s gates diverge: want %v, got %v", edge, gateSkills(w), gateSkills(d))
		}
		if w.Parallel != d.Parallel {
			t.Errorf("edge %s parallel diverges: want %d, got %d", edge, w.Parallel, d.Parallel)
		}
	}
	// Every scoped gate the authored workflow fires must still fire, for every
	// tag set — this is the "SOLE gating authority" property (sty_ca9f675f).
	for _, tags := range DefaultTagSets {
		for _, state := range authored.PerformingStates() {
			we, _ := authored.ScopedReviewersSplit(state, tags)
			de, _ := derived.ScopedReviewersSplit(state, tags)
			if !equalStrings(scopedStrings(we), scopedStrings(de)) {
				t.Errorf("scoped gates on %s (tags %v) diverge: want %v, got %v",
					state, tags, scopedStrings(we), scopedStrings(de))
			}
		}
		we, _ := authored.ScopedReviewersSplit("done", tags)
		de, _ := derived.ScopedReviewersSplit("done", tags)
		if !equalStrings(scopedStrings(we), scopedStrings(de)) {
			t.Errorf("scoped gates on done (tags %v) diverge: want %v, got %v",
				tags, scopedStrings(we), scopedStrings(de))
		}
	}
}
