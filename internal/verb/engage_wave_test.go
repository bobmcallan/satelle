package verb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// engageWaveWF is waveWF with a `plan` step ahead of in_progress, so a child can
// be mid-flight (engaging → engaging) as well as freshly engaged.
func engageWaveWF(schedule string) map[string]string {
	line := ""
	if schedule != "" {
		line = `schedule = "` + schedule + `"` + "\n"
	}
	return engageWaveWFLine(line)
}

// engageWaveWFLine is engageWaveWF with the container step's schedule line
// authored verbatim (e.g. the `sequential = true` override).
func engageWaveWFLine(line string) map[string]string {
	return routeHalves(
		`["*"]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "ready", "planned", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
`+line+`requires = ["raised"]

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

// wireEngageWave wires the fixture workflow. It returns the store.
func wireEngageWave(t *testing.T, schedule string) *store.DB {
	t.Helper()
	return wireEngageWaveWith(t, engageWaveWF(schedule))
}

// wireEngageWaveWith is wireEngageWave over an already-built workflow fixture.
func wireEngageWaveWith(t *testing.T, wf map[string]string) *store.DB {
	t.Helper()
	withWiring(t)
	return wireWithWorkflowsStore(t, wf)
}

// engageEpic makes the container and returns a child factory: each child carries
// the epic: theme tag AND parent_id, so wave membership and the sequential
// check both see it.
func engageWaveEpic(t *testing.T) (workitem.Item, func(tags ...string) workitem.Item) {
	t.Helper()
	epic := waveEpic(t)
	return epic, func(tags ...string) workitem.Item {
		return mkEpicStory(t, map[string]any{
			"category":  "fix",
			"parent_id": epic.ID,
			"tags":      append([]string{"epic:w"}, tags...),
		})
	}
}

func wantContains(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("must be refused, got nil")
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("refusal must contain %q: %v", p, err)
		}
	}
}

// AC1: under schedule=parallel a child waiting on a non-terminal dependency is
// refused, the error names the dependency, and the refused child does not perform.
func TestEngageRefusesChildWaitingOnDependency(t *testing.T) {
	wireEngageWave(t, "parallel")
	_, child := engageWaveEpic(t)
	dep := child()
	waiting := child("depends-on:" + dep.ID)

	err := setStatus(t, waiting.ID, "plan")
	wantContains(t, err, dep.ID, "not in the wave")
	if isPerforming(t, waiting.ID) {
		t.Errorf("a refused engagement must not perform")
	}
	got, _ := verb.Dispatch(context.Background(), "story-get", []byte(`{"id":"`+waiting.ID+`"}`))
	if !strings.Contains(string(got), `"status":"backlog"`) {
		t.Errorf("a refused child must stay in backlog: %s", got)
	}

	// The dependency itself is wave-eligible and engages.
	if err := setStatus(t, dep.ID, "plan"); err != nil {
		t.Fatalf("a dependency-free child must engage: %v", err)
	}
}

// AC1: two wave-eligible siblings both engage, from one tree or two: nothing
// holds a tree.
func TestEngageWaveEligibleSiblingsBothPerform(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEngageWave(t, "parallel")
	_, child := engageWaveEpic(t)
	a, b, c := child(), child(), child()

	for _, id := range []string{a.ID, b.ID} {
		if err := setStatus(t, id, "plan"); err != nil {
			t.Fatalf("sibling %s from the first tree: %v", id, err)
		}
	}
	chdir(t, treeB)
	if err := setStatus(t, c.ID, "plan"); err != nil {
		t.Fatalf("sibling from its own tree: %v", err)
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if !isPerforming(t, id) {
			t.Errorf("%s must be performing", id)
		}
	}
}

// AC2: under schedule=sequential two dependency-free children are both refused,
// naming both ids and the fix; once one depends on the other and that dependency
// is terminal, the remaining child engages.
func TestEngageSequentialRefusesWideWaveThenAdmitsAfterDependency(t *testing.T) {
	wireEngageWave(t, "sequential")
	_, child := engageWaveEpic(t)
	a, b := child(), child()

	for _, id := range []string{a.ID, b.ID} {
		wantContains(t, setStatus(t, id, "plan"), a.ID, b.ID, "depends-on")
	}

	// b now depends on a: a is the only runnable child, b waits on it.
	if _, err := verb.Dispatch(context.Background(), "story-set", []byte(`{"id":"`+b.ID+`","add_tags":["depends-on:`+a.ID+`"]}`)); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	wantContains(t, setStatus(t, b.ID, "plan"), a.ID)
	if err := setStatus(t, a.ID, "plan"); err != nil {
		t.Fatalf("the one runnable child must engage: %v", err)
	}
	if err := setStatus(t, a.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, a.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, b.ID, "plan"); err != nil {
		t.Fatalf("with its dependency terminal the remaining child must engage: %v", err)
	}
}

// A child already engaged is not re-judged at every later transition, even when
// the wave has since grown wider than a sequential epic allows.
func TestEngageMidFlightChildIsNotReRefused(t *testing.T) {
	wireEngageWave(t, "sequential")
	_, child := engageWaveEpic(t)
	a := child()
	if err := setStatus(t, a.ID, "plan"); err != nil {
		t.Fatalf("sole child: %v", err)
	}
	b := child() // the wave is now wide: a and b are both dependency-free

	if err := setStatus(t, a.ID, "in_progress"); err != nil {
		t.Fatalf("engaged child must keep moving: %v", err)
	}
	wantContains(t, setStatus(t, b.ID, "plan"), a.ID, b.ID, "depends-on")
}

// AC1: a container whose route declares no schedule is parallel — the wave
// returns every runnable child, and engagement enforces it like any parallel
// epic: a child waiting on a dependency is refused as off-wave.
func TestEngageUnscheduledContainerIsParallelByDefault(t *testing.T) {
	wireEngageWave(t, "")
	epic, child := engageWaveEpic(t)
	a := child()
	dep := child()
	b := child("depends-on:" + dep.ID)

	res, err := wave(t, epic.ID)
	if err != nil || res.Schedule != "parallel" || len(res.Runnable) != 2 {
		t.Fatalf("an unscheduled container must be parallel: %+v %v", res, err)
	}
	wantContains(t, setStatus(t, b.ID, "plan"), dep.ID, "not in the wave")
	if err := setStatus(t, a.ID, "plan"); err != nil {
		t.Errorf("a wave-eligible child of an unscheduled epic must engage: %v", err)
	}
}

// AC1: with no schedule setting, siblings of an epic perform alongside each
// other from distinct worktrees.
func TestEngageUnscheduledSiblingsPerformFromDistinctWorktrees(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEngageWave(t, "")
	_, child := engageWaveEpic(t)
	a, b := child(), child()

	if err := setStatus(t, a.ID, "plan"); err != nil {
		t.Fatalf("first sibling: %v", err)
	}
	chdir(t, treeB)
	if err := setStatus(t, b.ID, "plan"); err != nil {
		t.Fatalf("an unscheduled epic's second sibling must engage from its own worktree: %v", err)
	}
}

// AC4: stories that are not children of a scheduled epic engage as before.
func TestEngageNonChildIsUnaffected(t *testing.T) {
	wireEngageWave(t, "sequential")
	engageWaveEpic(t) // a scheduled container exists, but owns neither story below
	lone := mkEpicStory(t, map[string]any{"category": "fix"})
	plainParent := mkEpicStory(t, map[string]any{"category": "parent"})
	under := mkEpicStory(t, map[string]any{"category": "fix", "parent_id": plainParent.ID})

	if err := setStatus(t, lone.ID, "plan"); err != nil {
		t.Fatalf("parentless story: %v", err)
	}
	if err := setStatus(t, lone.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, lone.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, under.ID, "plan"); err != nil {
		t.Fatalf("child of a non-epic parent: %v", err)
	}
}

// A child linked only by parent_id is outside the wave's set (membership is the
// epic: tag), so the wave does not return it and engagement is refused.
func TestEngageParentLinkedChildOutsideEpicSetIsRefused(t *testing.T) {
	wireEngageWave(t, "parallel")
	epic, _ := engageWaveEpic(t)
	linked := mkEpicStory(t, map[string]any{"category": "fix", "parent_id": epic.ID})

	wantContains(t, setStatus(t, linked.ID, "plan"), "not in the epic set", epic.ID, "epic:w")
}
