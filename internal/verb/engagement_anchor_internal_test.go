package verb

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestRefuseForeignTreeDiffUnanchored (sty_c098dc2d AC7): the anchor rule is
// opt-in by construction. With no recorded tree — a baseline written before the
// anchor existed, or a non-git checkout — the diff proceeds exactly as it did,
// so upgrading cannot start refusing an in-flight story's own diff. The lease
// store is unwired here, which is also the "lease already released" shape.
func TestRefuseForeignTreeDiffUnanchored(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name             string
		baseline, invoke string
	}{
		{"no recorded tree", "", "/w/b"},
		{"no invocation tree", "/w/a", ""},
		{"neither", "", ""},
		{"same tree", "/w/a", "/w/a"},
	}
	for _, c := range cases {
		if err := refuseForeignTreeDiff(ctx, "sty_x", c.baseline, c.invoke); err != nil {
			t.Errorf("%s: must not refuse: %v", c.name, err)
		}
	}
}

// TestRefuseForeignTreeDiffAnchored: with both trees known and different, the
// refusal names BOTH — where the story is anchored and where the caller is —
// because the operator's next action is to re-run it in the named tree.
func TestRefuseForeignTreeDiffAnchored(t *testing.T) {
	err := refuseForeignTreeDiff(context.Background(), "sty_x", "/w/a", "/w/b")
	if err == nil {
		t.Fatal("foreign tree must be refused")
	}
	for _, want := range []string{"sty_x", "/w/a", "/w/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

// TestSeatKeyForModes (sty_c098dc2d AC1/AC2): the key policy in one place.
// Default mode keys every story on its own id — that IS single occupancy, not a
// special case. Epic mode keys on the parent, falling back to the item's own id
// when there is none (a singleton claim).
func TestSeatKeyForModes(t *testing.T) {
	t.Cleanup(ClearEngagementMode)
	child := workitem.Item{ID: "sty_child", Kind: workitem.KindStory, ParentID: "sty_epic"}
	lone := workitem.Item{ID: "sty_lone", Kind: workitem.KindStory}

	ClearEngagementMode()
	if got := seatKeyFor(child); got != "sty_child" {
		t.Errorf("default mode key = %q, want the item's own id", got)
	}
	if got := seatKeyFor(lone); got != "sty_lone" {
		t.Errorf("default mode key (parentless) = %q", got)
	}

	engagementParallel = "epic"
	if got := seatKeyFor(child); got != "sty_epic" {
		t.Errorf("epic mode key = %q, want the parent id", got)
	}
	if got := seatKeyFor(lone); got != "sty_lone" {
		t.Errorf("epic mode parentless key = %q, want a singleton claim on its own id", got)
	}
}
