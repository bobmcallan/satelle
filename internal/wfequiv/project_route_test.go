package wfequiv

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata goldens")

// projectRoute builds the derived route from the AUTHORED fixtures
// (internal/wfdot/testdata/done.md + step.md) through the production constructor.
// The prototype Go literal this replaced lived here only to answer "can the shape
// express it" (sty_c6184eaa); now that the constructor is real, the checker
// compares the real thing.
func projectRoute(t *testing.T) wfdot.Spec {
	t.Helper()
	dir := repoRoot()
	if dir == "" {
		t.Skip("no repo root in this checkout")
	}
	done, err := os.ReadFile(filepath.Join(dir, "internal", "wfdot", "testdata", "done.md"))
	if err != nil {
		t.Fatalf("read done.md: %v", err)
	}
	step, err := os.ReadFile(filepath.Join(dir, "internal", "wfdot", "testdata", "step.md"))
	if err != nil {
		t.Fatalf("read step.md: %v", err)
	}
	spec, err := wfdot.ParseRoute(string(done), string(step), "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return spec
}

// TestProjectRouteEquivalence asserts the derived route is behaviourally
// identical to the authored graph, and goldens the report either way.
//
// sty_c6184eaa could not assert emptiness — there a non-empty result WAS the
// deliverable, and asserting otherwise would have laundered the go/no-go signal.
// That question is settled, so this story holds the stronger line: any divergence
// at all is a regression, and the golden records what it was.
func TestProjectRouteEquivalence(t *testing.T) {
	authored := loadAuthored(t, "satelle-project-workflow.md")
	derived := projectRoute(t)
	report := Diff(authored, derived)

	var b strings.Builder
	b.WriteString("# satelle-project-workflow — authored DOT vs derived route\n\n")
	b.WriteString("## Divergence\n\n")
	b.WriteString(report.String())
	got := b.String()

	path := filepath.Join("testdata", "project-route.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated (%d divergences)", report.Count())
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if string(want) != got {
		t.Errorf("project-route golden drifted.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	if !report.Empty() {
		t.Errorf("derived route diverges from the authored graph:\n%s", report)
	}
}

// TestProjectRouteSpineReproduces asserts the ONE thing the epic's go/no-go turns
// on: the derived route must carry the same spine — the same performing states,
// the same edit-capable states, and the same reviewer gates admitting each. Fog
// around on_enter dispatch and other authored attributes is recorded in the
// golden; the spine is not negotiable.
func TestProjectRouteSpineReproduces(t *testing.T) {
	authored := loadAuthored(t, "satelle-project-workflow.md")
	derived := projectRoute(t)

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
