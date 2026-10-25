package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestWorktreeModeEditAttribution (sty_69ec7fbc AC3): under parallel = worktree
// several unrelated stories hold seats at once, one per tree, and a session may
// hold more than one. Each edit is attributed to the seat held from the edit's
// OWN tree — never by store order — and an edit in a tree that holds no seat
// resolves to no seat, which the gate refuses exactly as it did before the mode.
func TestWorktreeModeEditAttribution(t *testing.T) {
	a := seatInfo{ItemID: "sty_a", SessionID: "sess", Worktree: "/w/a"}
	b := seatInfo{ItemID: "sty_b", SessionID: "sess", Worktree: "/w/b"}
	c := seatInfo{ItemID: "sty_c", SessionID: "sess", Worktree: "/w/c"}
	for _, live := range [][]seatInfo{{a, b, c}, {c, b, a}, {b, c, a}} {
		stubWorktree(t, "/w/none")
		for _, want := range []seatInfo{a, b, c} {
			got, mine := pickSessionSeatFor(live, nil, "sess", want.Worktree)
			if got.ItemID != want.ItemID || !mine {
				t.Errorf("edit in %s: attributed to %q mine=%v, want %s", want.Worktree, got.ItemID, mine, want.ItemID)
			}
		}
		if got, _ := pickSessionSeatFor(live, nil, "sess", "/w/d"); got.ItemID != "" {
			t.Errorf("an edit in a tree with no seat must resolve to none, got %q", got.ItemID)
		}
	}
	// One session per seat, as separate agents in separate trees would run.
	solo := []seatInfo{
		{ItemID: "sty_a", SessionID: "sess-a", Worktree: "/w/a"},
		{ItemID: "sty_b", SessionID: "sess-b", Worktree: "/w/b"},
	}
	if got, mine := pickSessionSeatFor(solo, nil, "sess-b", "/w/b"); got.ItemID != "sty_b" || !mine {
		t.Errorf("session b: got %q mine=%v", got.ItemID, mine)
	}
}

// TestSeatModeLineWorktree: the seat inject states the worktree mode's one
// precondition, so an agent does not learn it from a refusal.
func TestSeatModeLineWorktree(t *testing.T) {
	line := seatModeLine(config.ParallelWorktree)
	for _, want := range []string{"worktree", "DISTINCT git working tree", "refused"} {
		if !strings.Contains(line, want) {
			t.Errorf("worktree mode line missing %q: %q", want, line)
		}
	}
}
