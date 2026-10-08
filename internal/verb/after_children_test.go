package verb_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
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
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelEpic}})
	return db
}

// acChild files a child of the epic: tagged into the set and linked by parent_id,
// so both set membership and the seat key see it.
func acChild(t *testing.T, epic workitem.Item, category string, tags ...string) workitem.Item {
	t.Helper()
	return mkEpicStory(t, map[string]any{
		"category":  category,
		"parent_id": epic.ID,
		"tags":      append([]string{"epic:w"}, tags...),
	})
}

// afterChildrenScenario builds the shared scene: the epic waits at ready; child
// done is terminal, child coded sits at in_progress holding a seat in treeB,
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
	// take a seat at ready and hold the very tree its children engage from.
	s.done = acChild(t, s.epic, "fix")
	s.coded = acChild(t, s.epic, "fix")
	s.cancelled = acChild(t, s.epic, "fix")
	s.open = acChild(t, s.epic, "fix")
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
	if _, held := leaseRow(t, s.db, s.epic.ID); held {
		t.Error("a refused transition must take no seat")
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

// AC2: while the container only waits it holds no seat; once every child has
// discharged (done, at/past the step, or cancelled) the transition proceeds and
// the performing step takes a seat under the epic key, in the engaged tree.
func TestAfterChildrenEntryTakesTheParentSeat(t *testing.T) {
	s := afterChildrenScenario(t)

	if _, held := leaseRow(t, s.db, s.epic.ID); held {
		t.Fatal("a container only waiting at ready must hold no lease")
	}
	moveTo(t, s.open.ID, "cancelled")

	moveTo(t, s.epic.ID, "merging")
	l, held := leaseRow(t, s.db, s.epic.ID)
	if !held {
		t.Fatal("entering a performing after_children step must acquire the parent's seat")
	}
	if l.State != "merging" || l.Worktree != s.treeA || l.SeatKey != s.epic.ID {
		t.Errorf("parent lease must record the step, tree and its own id as the key: %+v", l)
	}
	if child, ok := leaseRow(t, s.db, s.coded.ID); !ok || child.SeatKey != s.epic.ID {
		t.Errorf("the child shares the parent's key, so both hold: %+v %v", child, ok)
	}
}

// AC3: a child that has discharged the obligation engages its next step from its
// own worktree while the parent holds the seat; a second child in the parent's
// tree meets the ordinary tree conflict; a child the wave omits meets the wave's
// own refusal.
func TestAfterChildrenParentSeatLeavesDischargedChildFree(t *testing.T) {
	s := afterChildrenScenario(t)
	moveTo(t, s.open.ID, "cancelled")
	moveTo(t, s.epic.ID, "merging")

	chdir(t, s.treeB)
	moveTo(t, s.coded.ID, "integration")
	if l, ok := leaseRow(t, s.db, s.coded.ID); !ok || l.State != "integration" || l.Worktree != s.treeB {
		t.Errorf("the discharged child keeps moving from its own tree: %+v %v", l, ok)
	}

	// A child filed late, from the parent's own tree: the tree rule, nothing new.
	late := acChild(t, s.epic, "fix")
	chdir(t, s.treeA)
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": late.ID, "status": "plan"}); !errors.Is(err, lease.ErrTreeConflict) {
		t.Fatalf("a second story in the parent's tree must hit the tree conflict: %v", err)
	}

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
	db := wireAfterChildren(t, true)
	epic := waveEpic(t)
	c := acChild(t, epic, "fix")
	moveTo(t, epic.ID, "ready")
	moveTo(t, c.ID, "cancelled")
	if _, held := leaseRow(t, db, epic.ID); held {
		t.Fatal("a container only waiting at ready must hold no lease")
	}

	moveTo(t, epic.ID, "merging")
	if _, held := leaseRow(t, db, epic.ID); !held {
		t.Fatal("the performing step must take the seat")
	}
}

// AC4: with no after_children the container's route behaves as it did — the
// performing step is entered over open children and takes the lease it took
// before; the waiting step still holds none.
func TestNoAfterChildrenKeepsTheWaitsOnChildrenLeaseRule(t *testing.T) {
	_, _ = twoWorktrees(t)
	db := wireAfterChildren(t, false)
	epic := waveEpic(t)
	acChild(t, epic, "fix")

	moveTo(t, epic.ID, "ready")
	if _, held := leaseRow(t, db, epic.ID); held {
		t.Fatal("a container waiting on an open child holds no lease")
	}
	moveTo(t, epic.ID, "merging")
	if l, held := leaseRow(t, db, epic.ID); !held || l.State != "merging" {
		t.Fatalf("the performing step takes the lease it always took: %+v %v", l, held)
	}
}

// AC5: the step only gates entry and the seat. No production file this story
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
