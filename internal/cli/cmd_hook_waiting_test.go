package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// waitingRouteWFs is a fixture route with a container lane that idles at a step
// declaring waits_on_children, and a working lane whose implement step is
// allocated to a dispatched coder (sty_7f3e6fd3).
func waitingRouteWFs() []docindex.Doc {
	return routeWFs(
		`["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "children-resolved"]
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]

[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

func liveLease(id, state string, now time.Time) lease.Lease {
	return lease.Lease{
		ItemID: id, State: state, Owner: "alice",
		AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
	}
}

// TestEvaluateSeatContainerWaitingOnChildren (sty_7f3e6fd3 AC2): an epic at a
// route-declared waiting step with open children is not a performing seat — not
// live, and never named in a deny message for an unrelated edit.
func TestEvaluateSeatContainerWaitingOnChildren(t *testing.T) {
	// The deny text consults the repo for a dropped seat; give it an empty one.
	t.Chdir(tempRepo(t))
	wfs := waitingRouteWFs()
	now := time.Now().UTC()
	epic := workitem.Item{ID: "sty_epic", Kind: workitem.KindStory, Status: "ready", Category: "epic-parent", Tags: []string{"epic:wait"}}
	openChild := workitem.Item{ID: "sty_kid", Kind: workitem.KindStory, Status: "in_progress", Category: "feature", Tags: []string{"epic:wait"}}

	live, other, err := evaluateSeat([]lease.Lease{liveLease("sty_epic", "ready", now)},
		[]workitem.Item{epic, openChild}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("a container waiting on open children must not be a live seat: %+v", live)
	}
	if other.ItemID != "" {
		t.Fatalf("a waiting container must not be the naming pick: %+v", other)
	}
	reason := hookDenyReason(other, live, dispatchMarker{}, relayMarker{}, "sess", now)
	if strings.Contains(reason, "sty_epic") {
		t.Fatalf("deny message for an unrelated edit named the waiting epic: %s", reason)
	}

	// Every child terminal: nothing left to wait on, so it counts as performing.
	doneChild := openChild
	doneChild.Status = "done"
	live, _, err = evaluateSeat([]lease.Lease{liveLease("sty_epic", "ready", now)},
		[]workitem.Item{epic, doneChild}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ItemID != "sty_epic" {
		t.Fatalf("with every child terminal the container counts as performing again: %+v", live)
	}

	// No children at all: nothing to wait on.
	live, _, err = evaluateSeat([]lease.Lease{liveLease("sty_epic", "ready", now)},
		[]workitem.Item{epic}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("a container with no children is not waiting: %+v", live)
	}
}

// TestEvaluateSeatDispatchedStepWithChildTaskStillPerforms: the waiting signal
// is the route's, not a fact about having children — a story at a
// dispatched-performer step that has a child task still counts.
func TestEvaluateSeatDispatchedStepWithChildTaskStillPerforms(t *testing.T) {
	wfs := waitingRouteWFs()
	now := time.Now().UTC()
	story := workitem.Item{ID: "sty_work", Kind: workitem.KindStory, Status: "in_progress", Category: "feature"}
	task := workitem.Item{ID: "tsk_child", Kind: workitem.KindTask, Status: "in_progress", Category: "feature", ParentID: "sty_work"}

	live, _, err := evaluateSeat([]lease.Lease{liveLease("sty_work", "in_progress", now)},
		[]workitem.Item{story, task}, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ItemID != "sty_work" || !live[0].Engaged {
		t.Fatalf("a dispatched-coder story with a child task must stay performing: %+v", live)
	}
}

// TestDerivedSeatContainerWaitingOnChildren: the no-lease-store derivation
// applies the same rule.
func TestDerivedSeatContainerWaitingOnChildren(t *testing.T) {
	wfs := waitingRouteWFs()
	epic := workitem.Item{ID: "sty_epic", Kind: workitem.KindStory, Status: "ready", Category: "epic-parent", Tags: []string{"epic:wait"}}
	kid := workitem.Item{ID: "sty_kid", Kind: workitem.KindStory, Status: "backlog", Category: "feature", Tags: []string{"epic:wait"}}
	info, engaged, err := derivedSeat([]workitem.Item{epic, kid}, wfs)
	if err != nil {
		t.Fatal(err)
	}
	if engaged || info.ItemID == "sty_epic" {
		t.Fatalf("a waiting container must not derive a seat: %+v engaged=%v", info, engaged)
	}
}

// TestEditPermittedDispatchedCoderOnCorrectSeat (sty_7f3e6fd3 AC3): the case
// reproduced — a coder dispatched at its step, on a live seat whose committed
// status is that step but with no transition in flight, was refused because
// editPermitted required InFlight for any dispatch marker.
func TestEditPermittedDispatchedCoderOnCorrectSeat(t *testing.T) {
	base := seatInfo{
		ItemID: "sty_x", State: "in_progress", TargetState: "in_progress", StoryStatus: "in_progress",
		StateAgent: "coder", Engaged: true, EditCapable: false,
		DispatchAgents: map[string][]string{"in_progress": {"coder"}, "plan": {"planner"}},
	}
	coder := dispatchMarker{Agent: "coder", Step: "in_progress", Item: "sty_x"}
	cases := []struct {
		name   string
		info   seatInfo
		marker dispatchMarker
		want   bool
	}{
		{"coder on its committed step", base, coder, true},
		{"coder while the entry transition is in flight", withSeat(base, func(s *seatInfo) {
			s.InFlight = true
			s.StoryStatus = "plan"
		}), coder, true},
		{"wrong item", base, dispatchMarker{Agent: "coder", Step: "in_progress", Item: "sty_other"}, false},
		{"step is not the committed status", withSeat(base, func(s *seatInfo) {
			s.StoryStatus = "plan"
			s.State = "plan"
		}), coder, false},
		{"agent not allocated to the step", base, dispatchMarker{Agent: "planner", Step: "in_progress", Item: "sty_x"}, false},
		{"stale seat", withSeat(base, func(s *seatInfo) { s.Stale = true }), coder, false},
		{"seat not engaged", withSeat(base, func(s *seatInfo) { s.Engaged = false }), coder, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := editPermitted(tc.info, tc.marker); got != tc.want {
				t.Fatalf("editPermitted(%+v, %+v) = %v, want %v", tc.info, tc.marker, got, tc.want)
			}
			if got := hookEditPermitted(tc.info, tc.marker, relayMarker{}); got != tc.want {
				t.Fatalf("hookEditPermitted = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFirstDroppedPerformingSeatIgnoresWaitingContainer: an epic parked at its
// waiting step with no seat row is not a "dropped performing seat" that makes
// the hook refuse unrelated commands — until its children are all resolved.
func TestFirstDroppedPerformingSeatIgnoresWaitingContainer(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range waitingRouteWFs() {
		if err := os.WriteFile(filepath.Join(wfDir, d.Name+".toml"), []byte(d.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, now); err != nil {
		t.Fatal(err)
	}
	epic, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "epic", Body: "goal", AcceptanceCriteria: "1. ok",
		Status: "ready", Category: "epic-parent", Tags: []string{"epic:wait"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "kid", Body: "goal", AcceptanceCriteria: "1. ok",
		Status: "backlog", Category: "feature", Tags: []string{"epic:wait"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if drop := droppedPerformingSeats(); len(drop) != 0 {
		t.Fatalf("a waiting container was treated as a dropped performing seat: %+v", drop)
	}

	done := "done"
	if _, err := db.Stories.Update(ctx, kid.ID, workitem.UpdateInput{Status: &done}, now); err != nil {
		t.Fatal(err)
	}
	if drop := droppedPerformingSeats(); len(drop) != 1 || drop[0].ItemID != epic.ID {
		t.Fatalf("with every child terminal the container is performing again: %+v", drop)
	}
}
