package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// coHeldSeatFixture: two sibling stories performing under one arbitration key,
// each anchored to its own working tree — the shape every "seat held?" reader
// must handle once a repo admits co-holders (sty_c098dc2d AC5).
func coHeldSeatFixture(now time.Time) ([]lease.Lease, []workitem.Item, []docindex.Doc) {
	wfs := routeWFs(`["*"]
obligations = ["raised", "coded", "closed"]
`, `[raised]
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
`)
	leases := []lease.Lease{
		{
			ItemID: "sty_a", State: "in_progress", Owner: "local@host",
			SeatKey: "sty_epic", Worktree: "/w/a", StorySeat: true,
			AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
		},
		{
			ItemID: "sty_b", State: "in_progress", Owner: "local@host",
			SeatKey: "sty_epic", Worktree: "/w/b", StorySeat: true,
			AcquiredAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
		},
	}
	items := []workitem.Item{
		{ID: "sty_a", Kind: workitem.KindStory, Status: "in_progress", Category: "feature", ParentID: "sty_epic"},
		{ID: "sty_b", Kind: workitem.KindStory, Status: "in_progress", Category: "feature", ParentID: "sty_epic"},
	}
	return leases, items, wfs
}

// TestEvaluateSeatReturnsEveryLiveSeat (sty_c098dc2d AC5): the reader that used
// to return the FIRST qualifying seat and drop the rest now returns them all.
// "Is work in flight?" stays one question — len(live) > 0 — whether the answer
// is one lease or five.
func TestEvaluateSeatReturnsEveryLiveSeat(t *testing.T) {
	now := time.Now().UTC()
	leases, items, wfs := coHeldSeatFixture(now)
	live, other, err := evaluateSeat(leases, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 {
		t.Fatalf("both co-held seats must be live, got %d: %+v", len(live), live)
	}
	for _, s := range live {
		if !s.Engaged {
			t.Errorf("live seat not marked engaged: %+v", s)
		}
	}
	if live[0].Worktree != "/w/a" || live[1].Worktree != "/w/b" {
		t.Errorf("live seats must carry their tree anchors: %+v", live)
	}
	if other.ItemID != "" {
		t.Errorf("no residue expected: %+v", other)
	}

	// A task lease alongside them is NOT a story seat and must not appear as one.
	withTask := append(leases, lease.Lease{
		ItemID: "tsk_1", State: "in_progress", Owner: "local@host", Kind: "task",
		AcquiredAt: now.Add(-time.Minute), HeartbeatAt: now,
	})
	live, _, err = evaluateSeat(withTask, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 {
		t.Fatalf("task lease must not join the story seats: %+v", live)
	}
}

// TestPickSessionSeatRoutesByWorktree (sty_c098dc2d AC4/AC5): with several live
// seats, the session's own is chosen by working tree — the pid-less local@host
// owner cannot discriminate them. This selection is what the heartbeat rides on,
// so getting it wrong lets the actively-worked sibling go stale at TTL.
func TestPickSessionSeatRoutesByWorktree(t *testing.T) {
	now := time.Now().UTC()
	leases, items, wfs := coHeldSeatFixture(now)
	live, _, err := evaluateSeat(leases, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	orig := sessionWorktree
	t.Cleanup(func() { sessionWorktree = orig })

	sessionWorktree = func() string { return "/w/b" }
	if got, mine := pickSessionSeat(live, ""); got.ItemID != "sty_b" || mine {
		t.Errorf("session in /w/b must pick sty_b mine=false, got %+v mine=%v", got, mine)
	}
	sessionWorktree = func() string { return "/w/a" }
	if got, mine := pickSessionSeat(live, ""); got.ItemID != "sty_a" || mine {
		t.Errorf("session in /w/a must pick sty_a mine=false, got %+v mine=%v", got, mine)
	}
	// No tree answer (non-git session) falls back to the first live seat rather
	// than reporting no seat at all — the gate question must stay answered.
	sessionWorktree = func() string { return "" }
	if got, mine := pickSessionSeat(live, ""); got.ItemID != "sty_a" || mine {
		t.Errorf("unresolvable tree must fall back to the first live seat: %+v mine=%v", got, mine)
	}
	if got, _ := pickSessionSeat(nil, ""); got.ItemID != "" {
		t.Errorf("no live seats must yield no pick: %+v", got)
	}
}

func TestSameTreeSessionsDoNotShareSeat(t *testing.T) {
	a := seatInfo{ItemID: "sty_a", Worktree: "/w/a", SessionID: "sess-A", InFlight: true}
	live := []seatInfo{a}
	orig := sessionWorktree
	t.Cleanup(func() { sessionWorktree = orig })
	sessionWorktree = func() string { return "/w/a" }

	got, mine := pickSessionSeat(live, "sess-A")
	if got.ItemID != "sty_a" || !mine {
		t.Fatalf("driver must pick sty_a mine=true, got %+v mine=%v", got, mine)
	}
	got, mine = pickSessionSeat(live, "sess-B")
	if got.ItemID != "" || mine {
		t.Fatalf("sibling must not inherit stamped seat, got %+v mine=%v", got, mine)
	}
	got, mine = pickSessionSeat([]seatInfo{{ItemID: "sty_a", Worktree: "/w/a"}}, "sess-B")
	if got.ItemID != "sty_a" || mine {
		t.Fatalf("unstamped non-flight tree match is mine=false, got %+v mine=%v", got, mine)
	}
	got, mine = pickSessionSeat([]seatInfo{{ItemID: "sty_a", Worktree: "/w/a", InFlight: true}}, "sess-B")
	if got.ItemID != "sty_a" || mine {
		t.Fatalf("unstamped in-flight still tree-routes mine=false, got %+v mine=%v", got, mine)
	}
}

// TestSessionSeatBlockRendersEveryLiveSeat (sty_c098dc2d AC5): the SessionStart
// inject used to fall back to leases[0]. An operator opening a session where
// several stories are in flight must see them all, led by their own.
func TestSessionSeatBlockRendersEveryLiveSeat(t *testing.T) {
	now := time.Now().UTC()
	leases, items, wfs := coHeldSeatFixture(now)
	live, _, err := evaluateSeat(leases, items, wfs, now)
	if err != nil {
		t.Fatal(err)
	}
	orig := sessionWorktree
	t.Cleanup(func() { sessionWorktree = orig })
	sessionWorktree = func() string { return "/w/b" }

	out := renderSeatBlocks(live, now, config.ParallelEpic)
	if !strings.Contains(out, "sty_a") || !strings.Contains(out, "sty_b") {
		t.Fatalf("session block must name every live seat: %s", out)
	}
	if strings.Index(out, "sty_b") > strings.Index(out, "sty_a") {
		t.Errorf("this session's own seat must lead: %s", out)
	}
}
