package lease

import (
	"context"
	"errors"
	"testing"
)

// TestCoHolds (sty_69ec7fbc): the one co-hold rule. Claims that are not both
// tree-scoped co-hold only on equal non-empty keys (the rule before the flags);
// tree-scoped claims co-hold unless they share an exclusive key.
func TestCoHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b SeatClaim
		want bool
	}{
		{"legacy equal keys", SeatClaim{Key: "e"}, SeatClaim{Key: "e"}, true},
		{"legacy different keys", SeatClaim{Key: "x"}, SeatClaim{Key: "y"}, false},
		{"legacy empty keys", SeatClaim{}, SeatClaim{}, false},
		{"legacy holder, tree claimant, different keys", SeatClaim{Key: "x"}, SeatClaim{Key: "y", TreeScoped: true}, false},
		{"tree/tree different keys", SeatClaim{Key: "x", TreeScoped: true}, SeatClaim{Key: "y", TreeScoped: true}, true},
		{"tree/tree same key", SeatClaim{Key: "e", TreeScoped: true}, SeatClaim{Key: "e", TreeScoped: true}, true},
		{"tree/tree same key, one exclusive", SeatClaim{Key: "e", TreeScoped: true, Exclusive: true}, SeatClaim{Key: "e", TreeScoped: true}, false},
		{"tree/tree same key, both exclusive", SeatClaim{Key: "e", TreeScoped: true, Exclusive: true}, SeatClaim{Key: "e", TreeScoped: true, Exclusive: true}, false},
		{"tree/tree different keys, exclusive", SeatClaim{Key: "e", TreeScoped: true, Exclusive: true}, SeatClaim{Key: "x", TreeScoped: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CoHolds(tc.a, tc.b); got != tc.want {
				t.Errorf("CoHolds(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := CoHolds(tc.b, tc.a); got != tc.want {
				t.Errorf("CoHolds must be symmetric: (%+v, %+v) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func treeScopedOpts(item, key, tree string, exclusive bool) AcquireOpts {
	return AcquireOpts{
		ItemID: item, Kind: "story", Owner: "alice", State: "plan", StorySeat: true,
		SeatKey: key, TreeScoped: true, Exclusive: exclusive, Worktree: tree,
	}
}

// TestAcquireTreeScoped (sty_69ec7fbc AC1): tree-scoped claims under different
// keys hold the seat together from distinct trees; a second claim from an
// occupied tree is a tree conflict naming the holder; flags round-trip the store.
func TestAcquireTreeScoped(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	for i, o := range []AcquireOpts{
		treeScopedOpts("sty_a", "epic1", "/w/a", false),
		treeScopedOpts("sty_b", "epic2", "/w/b", false),
		treeScopedOpts("sty_c", "sty_c", "/w/c", false),
	} {
		if _, out, _, err := s.AcquireWith(ctx, o); err != nil || out != OutcomeAcquired {
			t.Fatalf("claim %d (%s): out=%v err=%v", i, o.ItemID, out, err)
		}
	}
	_, out, holder, err := s.AcquireWith(ctx, treeScopedOpts("sty_d", "epic1", "/w/b", false))
	if err != nil || out != OutcomeTreeConflict || holder == nil || holder.ItemID != "sty_b" {
		t.Fatalf("occupied tree: out=%v holder=%+v err=%v, want a tree conflict naming sty_b", out, holder, err)
	}
	got, err := s.Get(ctx, "sty_a")
	if err != nil || !got.TreeScoped || got.Exclusive {
		t.Fatalf("tree-scoped flag must persist: %+v err=%v", got, err)
	}
}

// TestAcquireExclusiveKey (sty_69ec7fbc AC2): a shared exclusive key admits one,
// as a key conflict (a free tree does not help), while an unrelated tree-scoped
// claim still co-holds.
func TestAcquireExclusiveKey(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	if _, out, _, err := s.AcquireWith(ctx, treeScopedOpts("sty_a", "epic1", "/w/a", true)); err != nil || out != OutcomeAcquired {
		t.Fatalf("first: out=%v err=%v", out, err)
	}
	_, out, holder, err := s.AcquireWith(ctx, treeScopedOpts("sty_b", "epic1", "/w/b", true))
	if err != nil || out != OutcomeConflict || holder == nil || holder.ItemID != "sty_a" {
		t.Fatalf("sibling of an exclusive key: out=%v holder=%+v err=%v", out, holder, err)
	}
	if _, out, _, err := s.AcquireWith(ctx, treeScopedOpts("sty_c", "sty_c", "/w/c", false)); err != nil || out != OutcomeAcquired {
		t.Fatalf("unrelated claim must co-hold: out=%v err=%v", out, err)
	}
}

// TestAcquireTreeScopedRequiresTree: tree identity is the whole arbitration, so
// a tree-scoped claim with no tree is refused rather than guessed.
func TestAcquireTreeScopedRequiresTree(t *testing.T) {
	s := openTestDB(t)
	_, _, _, err := s.AcquireWith(context.Background(), treeScopedOpts("sty_a", "epic1", "", false))
	if !errors.Is(err, ErrTreeRequired) {
		t.Fatalf("err = %v, want ErrTreeRequired", err)
	}
}

// TestAcquireLegacyHolderBlocksTreeScoped: a holder from before the flags (or
// from a non-worktree mode) is not tree-scoped, so a tree-scoped claim under a
// different key still conflicts — concurrency is never widened by accident.
func TestAcquireLegacyHolderBlocksTreeScoped(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	if _, out, _, err := s.Acquire(ctx, "sty_a", "story", "alice", "plan", true); err != nil || out != OutcomeAcquired {
		t.Fatalf("legacy holder: out=%v err=%v", out, err)
	}
	_, out, _, err := s.AcquireWith(ctx, treeScopedOpts("sty_b", "epic1", "/w/b", false))
	if err != nil || out != OutcomeConflict {
		t.Fatalf("out=%v err=%v, want a conflict against the non-tree-scoped holder", out, err)
	}
}
