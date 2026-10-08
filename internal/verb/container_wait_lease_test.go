package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// containerWaitWF is a route whose container lane idles at a step declaring
// waits_on_children (sty_56648ae5), between a performing step and the close.
// Working stories take the ordinary `["*"]` lane.
var containerWaitWF = routeHalves(
	`["*"]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked" }

[container]
obligations = ["raised", "planned", "ready", "wrapped"]
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

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["planned"]

[wrapped]
status = "in_review"
agent = "executor"
requires = ["ready"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

// wireContainerWait wires the waiting route under the epic seat mode and
// returns the store so a test can read the lease table directly.
func wireContainerWait(t *testing.T) *store.DB {
	t.Helper()
	withWiring(t)
	db := wireWithWorkflowsStore(t, containerWaitWF)
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelEpic}})
	return db
}

func createContainer(t *testing.T) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "Container", "category": "container"}), &it)
	return it
}

func moveTo(t *testing.T, id, status string) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": id, "status": status}), &it)
	if it.Status != status {
		t.Fatalf("%s → %s: got %q", id, status, it.Status)
	}
	return it
}

func leaseRow(t *testing.T, db *store.DB, id string) (lease.Lease, bool) {
	t.Helper()
	l, err := db.Leases.Get(context.Background(), id)
	if err != nil {
		return lease.Lease{}, false
	}
	return l, true
}

// TestContainerWaitingHoldsNoLease (AC1): a container entering a
// waits_on_children status with a child open holds no story-seat row, and the
// row its prior performing step left is released by that very transition.
func TestContainerWaitingHoldsNoLease(t *testing.T) {
	tree, _ := twoWorktrees(t)
	chdir(t, tree)
	db := wireContainerWait(t)

	c := createContainer(t)
	createChild(t, "Child", c.ID)

	moveTo(t, c.ID, "plan")
	if _, ok := leaseRow(t, db, c.ID); !ok {
		t.Fatal("a performing container step must hold a lease")
	}
	moveTo(t, c.ID, "ready")
	if l, ok := leaseRow(t, db, c.ID); ok {
		t.Fatalf("a container waiting on an open child must hold no lease: %+v", l)
	}
}

// TestContainerWaitingFreesTreeForOneChild (AC2, AC3): from the tree the
// container waited in, one child engages under the epic key and a second is
// refused with the one-engagement-per-tree error.
func TestContainerWaitingFreesTreeForOneChild(t *testing.T) {
	tree, _ := twoWorktrees(t)
	chdir(t, tree)
	db := wireContainerWait(t)

	c := createContainer(t)
	a := createChild(t, "Child A", c.ID)
	b := createChild(t, "Child B", c.ID)
	moveTo(t, c.ID, "plan")
	moveTo(t, c.ID, "ready")

	moveTo(t, a.ID, "plan")
	l, ok := leaseRow(t, db, a.ID)
	if !ok {
		t.Fatal("the child must hold the seat")
	}
	if l.SeatKey != c.ID || l.Worktree != tree {
		t.Fatalf("child lease must sit under the epic key in this tree: %+v", l)
	}

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": b.ID, "status": "plan"})
	if !errors.Is(err, lease.ErrTreeConflict) {
		t.Fatalf("a second child in the same tree must hit the tree conflict: %v", err)
	}
}

// TestContainerResumesPerformingWhenChildrenResolved (AC4): once every child is
// terminal the container's next performing step takes a lease normally.
func TestContainerResumesPerformingWhenChildrenResolved(t *testing.T) {
	tree, _ := twoWorktrees(t)
	chdir(t, tree)
	db := wireContainerWait(t)

	c := createContainer(t)
	a := createChild(t, "Child A", c.ID)
	moveTo(t, c.ID, "plan")
	moveTo(t, c.ID, "ready")
	moveTo(t, a.ID, "plan")
	moveTo(t, a.ID, "in_progress")
	moveTo(t, a.ID, "done")

	moveTo(t, c.ID, "in_review")
	l, ok := leaseRow(t, db, c.ID)
	if !ok {
		t.Fatal("the container's next performing step must acquire a lease")
	}
	if l.State != "in_review" || l.Worktree != tree {
		t.Fatalf("lease must record the committed step and tree: %+v", l)
	}
}

// TestWaitRuleKeepsOrdinarySeats (AC5): the wait rule keys off the route's
// declaration, not container-ness. A childless working story, and a container
// at a NON-waiting performing step with open children, both still hold seats.
func TestWaitRuleKeepsOrdinarySeats(t *testing.T) {
	tree, _ := twoWorktrees(t)
	chdir(t, tree)
	db := wireContainerWait(t)

	lone := createChild(t, "Lone", "")
	moveTo(t, lone.ID, "plan")
	if _, ok := leaseRow(t, db, lone.ID); !ok {
		t.Fatal("an ordinary story entering an engaging status must hold a lease")
	}
	moveTo(t, lone.ID, "in_progress")
	moveTo(t, lone.ID, "done")

	c := createContainer(t)
	createChild(t, "Child", c.ID)
	moveTo(t, c.ID, "plan")
	if _, ok := leaseRow(t, db, c.ID); !ok {
		t.Fatal("a container at a non-waiting performing step must hold a lease")
	}
}
