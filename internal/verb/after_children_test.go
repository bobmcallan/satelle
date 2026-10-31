package verb_test

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// afterChildrenWF is a scheduled epic whose container lane waits on its children
// (ready), then enters a performing `merging` step. With afterChildren the step
// declares the child obligation it waits on (sty_fdad98b0); without it the step
// is the plain performing step the container route had before.
func afterChildrenWF(afterChildren bool) map[string]string {
	declare := ""
	if afterChildren {
		declare = `after_children = "coded"` + "\n"
	}
	return routeHalves(
		`["*"]
obligations = ["raised", "planned", "coded", "integrated", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "ready", "merged", "parent-closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[chore]
obligations = ["raised", "chore-done"]
cancel = { state = "cancelled" }
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "executor"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["planned"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["integrated"]

[chore-done]
status = "done"
terminal = true
requires = ["raised"]

[ready]
status = "ready"
waits_on_children = true
schedule = "parallel"
requires = ["raised"]

[merged]
status = "merging"
agent = "executor"
`+declare+`requires = ["ready"]

[parent-closed]
status = "done"
agent = "reviewer"
terminal = true
requires = ["merged"]
`)
}

func wireAfterChildren(t *testing.T, afterChildren bool) *store.DB {
	t.Helper()
	withWiring(t)
	db := wireWithWorkflowsStore(t, afterChildrenWF(afterChildren))
	return db
}

// acChild files a child of the epic: tagged into the set and linked by parent_id,
// so both set membership and the wave see it.
func acChild(t *testing.T, epic workitem.Item, category string, tags ...string) workitem.Item {
	t.Helper()
	return mkEpicStory(t, map[string]any{
		"category":  category,
		"parent_id": epic.ID,
		"tags":      append([]string{"epic:w"}, tags...),
	})
}

// afterChildrenScenario builds the shared scene: the epic waits at ready; child
// done is terminal, child coded sits at in_progress engaged from treeB,
// child cancelled is cancelled, and child open is still in backlog. It leaves
// the working directory in treeA.
type afterChildrenScene struct {
	db                                 *store.DB
	treeA, treeB                       string
	epic, done, coded, cancelled, open workitem.Item
}

func afterChildrenScenario(t *testing.T) afterChildrenScene {
	t.Helper()
	s := afterChildrenScene{}
	s.treeA, s.treeB = twoWorktrees(t)
	chdir(t, s.treeA)
	s.db = wireAfterChildren(t, true)
	s.epic = waveEpic(t)
	// Children first: a container with no open child is not waiting, so it would
	// perform at ready rather than wait on them.
	s.done = acChild(t, s.epic, "fix")
	s.coded = acChild(t, s.epic, "fix")
	s.cancelled = acChild(t, s.epic, "fix")
	s.open = acChild(t, s.epic, "fix")
	acceptEpicReadiness(t, s.db, s.epic.ID, "backlog", "ready")
	moveTo(t, s.epic.ID, "ready")

	for _, st := range []string{"plan", "in_progress", "integration", "done"} {
		moveTo(t, s.done.ID, st)
	}
	chdir(t, s.treeB)
	moveTo(t, s.coded.ID, "plan")
	moveTo(t, s.coded.ID, "in_progress")
	moveTo(t, s.cancelled.ID, "cancelled")
	chdir(t, s.treeA)
	return s
}

// AC1: the transition is refused while a child has not discharged the obligation,
// the refusal names exactly those children, and nothing moves.
func TestAfterChildrenRefusesNamingUndischargedChildren(t *testing.T) {
	s := afterChildrenScenario(t)

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": s.epic.ID, "status": "merging"})
	if err == nil {
		t.Fatal("entry must be refused while a child has not discharged the obligation")
	}
	msg := err.Error()
	for _, want := range []string{s.open.ID, "backlog", `"coded"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal must contain %q: %v", want, err)
		}
	}
	for _, not := range []string{s.done.ID, s.coded.ID, s.cancelled.ID} {
		if strings.Contains(msg, not) {
			t.Errorf("refusal must not name %s, which has discharged it: %v", not, err)
		}
	}
	if got := statusOf(t, s.epic.ID).Status; got != "ready" {
		t.Errorf("a refused container stays at ready, got %q", got)
	}
	if p, ok := presenceRow(t, s.epic.ID); ok && p.State == "merging" {
		t.Error("a refused transition must record no working_on row")
	}
}

// AC1: a child whose own route has no step providing the obligation is named,
// with the reason.
func TestAfterChildrenNamesAChildWhoseRouteLacksTheObligation(t *testing.T) {
	_, _ = twoWorktrees(t)
	wireAfterChildren(t, true)
	epic := waveEpic(t)
	moveTo(t, epic.ID, "ready")
	odd := acChild(t, epic, "chore")

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": epic.ID, "status": "merging"})
	if err == nil || !strings.Contains(err.Error(), odd.ID) || !strings.Contains(err.Error(), `no step providing "coded"`) {
		t.Fatalf("refusal must name %s and say its route has no step providing the obligation: %v", odd.ID, err)
	}
}

// AC1: a story whose epic set cannot be fixed does not enter the step.
func TestAfterChildrenRefusesAnUndeterminedSet(t *testing.T) {
	_, _ = twoWorktrees(t)
	wireAfterChildren(t, true)
	untagged := mkEpicStory(t, map[string]any{"category": "epic-parent"})
	moveTo(t, untagged.ID, "ready")

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": untagged.ID, "status": "merging"})
	if err == nil || !strings.Contains(err.Error(), "undetermined") {
		t.Fatalf("an epic with no resolvable set must refuse: %v", err)
	}
}

// AC2: while the container only waits it is not performing; once every child
// has discharged (done, at/past the step, or cancelled) the transition proceeds
// and the performing step records its presence in the engaged tree.
func TestAfterChildrenEntryPerformsTheParent(t *testing.T) {
	s := afterChildrenScenario(t)

	if isPerforming(t, s.epic.ID) {
		t.Fatal("a container only waiting at ready must not be performing")
	}
	moveTo(t, s.open.ID, "cancelled")

	moveTo(t, s.epic.ID, "merging")
	if !isPerforming(t, s.epic.ID) {
		t.Fatal("entering a performing after_children step must perform the parent")
	}
	if p, ok := presenceRow(t, s.epic.ID); !ok || p.State != "merging" || p.Worktree != s.treeA {
		t.Errorf("parent working_on must record the step and tree: %+v %v", p, ok)
	}
	if !isPerforming(t, s.coded.ID) {
		t.Error("the child keeps performing beside the parent")
	}
}

// AC3: a child that has discharged the obligation engages its next step from its
// own worktree while the parent performs; a second child engages from the
// parent's tree, since nothing holds a tree; a child the wave omits meets the
// wave's own refusal.
func TestAfterChildrenPerformingParentLeavesDischargedChildFree(t *testing.T) {
	s := afterChildrenScenario(t)
	moveTo(t, s.open.ID, "cancelled")
	moveTo(t, s.epic.ID, "merging")

	chdir(t, s.treeB)
	moveTo(t, s.coded.ID, "integration")
	if p, ok := presenceRow(t, s.coded.ID); !ok || p.State != "integration" || p.Worktree != s.treeB {
		t.Errorf("the discharged child keeps moving from its own tree: %+v %v", p, ok)
	}

	// A child filed late engages from the parent's own tree: no tree is held.
	late := acChild(t, s.epic, "fix")
	chdir(t, s.treeA)
	moveTo(t, late.ID, "plan")

	// A late child that waits on an unfinished sibling is off the wave: refused by
	// the wave's own rule and wording, never an after_children message.
	waiting := acChild(t, s.epic, "fix", "depends-on:"+late.ID)
	chdir(t, s.treeB)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": waiting.ID, "status": "plan"})
	if err == nil || !strings.Contains(err.Error(), "not in the wave") || !strings.Contains(err.Error(), late.ID) {
		t.Fatalf("an off-wave child must be refused by the wave rule: %v", err)
	}
	if strings.Contains(err.Error(), "after_children") || strings.Contains(err.Error(), "discharged") {
		t.Errorf("the wave refusal must not carry after_children wording: %v", err)
	}
}

// AC2: nothing waits on a child that is already past the obligation or cancelled.
func TestAfterChildrenAllResolvedOrPastProceeds(t *testing.T) {
	_, _ = twoWorktrees(t)
	wireAfterChildren(t, true)
	epic := waveEpic(t)
	c := acChild(t, epic, "fix")
	moveTo(t, epic.ID, "ready")
	if isPerforming(t, epic.ID) {
		t.Fatal("a container only waiting at ready must not be performing")
	}
	moveTo(t, c.ID, "cancelled")

	moveTo(t, epic.ID, "merging")
	if !isPerforming(t, epic.ID) {
		t.Fatal("the performing step must perform the container")
	}
}

// AC4: with no after_children the container's route behaves as it did — the
// performing step is entered over open children and performs; the waiting step
// still does not.
func TestNoAfterChildrenKeepsTheWaitsOnChildrenRule(t *testing.T) {
	_, _ = twoWorktrees(t)
	wireAfterChildren(t, false)
	epic := waveEpic(t)
	acChild(t, epic, "fix")

	moveTo(t, epic.ID, "ready")
	if isPerforming(t, epic.ID) {
		t.Fatal("a container waiting on an open child is not performing")
	}
	moveTo(t, epic.ID, "merging")
	if p, ok := presenceRow(t, epic.ID); !isPerforming(t, epic.ID) || !ok || p.State != "merging" {
		t.Fatalf("the performing step performs as it always did: %+v %v", p, ok)
	}
}

// AC5: the step only gates entry. No production file this story
// added runs git, merges, pushes or bumps a version.
func TestAfterChildrenRunsNoGitMergePushOrVersionBump(t *testing.T) {
	forbidden := []string{"os/exec", "exec.Command", "git merge", "git push", "git commit", ".version"}
	for _, path := range []string{
		"after_children.go",
		"../wfgovern/childresolved.go",
		"../wfdot/route_parse.go",
		"../wfdot/route.go",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range forbidden {
			if strings.Contains(string(body), f) {
				t.Errorf("%s must not contain %q", path, f)
			}
		}
	}
}
