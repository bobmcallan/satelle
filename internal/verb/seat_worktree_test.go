package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// addWorktree opens one more linked tree off treeA and returns its toplevel.
func addWorktree(t *testing.T, treeA, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("git", "worktree", "add", "-b", name, dir)
	cmd.Dir = treeA
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", name, err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("toplevel %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// TestWorktreeSeatAcrossEpicsAndNone (sty_69ec7fbc AC1/AC4): under worktree, two
// stories from two different epics and one with no epic engage at once, each from
// its own tree; a second engagement from an occupied tree is refused as a tree
// conflict naming the holder.
func TestWorktreeSeatAcrossEpicsAndNone(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	treeC := addWorktree(t, treeA, "tree-c")
	treeD := addWorktree(t, treeA, "tree-d")
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelWorktree)

	epic1 := createChild(t, "Epic one", "")
	epic2 := createChild(t, "Epic two", "")
	inEpic1 := createChild(t, "In epic one", epic1.ID)
	inEpic2 := createChild(t, "In epic two", epic2.ID)
	lone := createChild(t, "No epic", "")
	extra := createChild(t, "Extra", "")

	for _, step := range []struct {
		tree string
		item workitem.Item
	}{{treeA, inEpic1}, {treeB, inEpic2}, {treeC, lone}} {
		chdir(t, step.tree)
		var got workitem.Item
		json.Unmarshal(call(t, "story-set", map[string]any{"id": step.item.ID, "status": "plan"}), &got)
		if got.Status != "plan" {
			t.Fatalf("%s from %s must engage under worktree: %q", step.item.ID, step.tree, got.Status)
		}
	}
	seats := string(mustSeatList(t))
	for _, id := range []string{inEpic1.ID, inEpic2.ID, lone.ID} {
		if !strings.Contains(seats, id) {
			t.Errorf("seat for %s missing: %s", id, seats)
		}
	}

	// A fourth story from an OCCUPIED tree is refused, naming the holder.
	chdir(t, treeB)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": extra.ID, "status": "plan"})
	if err == nil {
		t.Fatal("a second engagement from an occupied tree must be refused")
	}
	if !errors.Is(err, lease.ErrTreeConflict) {
		t.Fatalf("refusal must carry the tree-conflict sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), inEpic2.ID) || !strings.Contains(err.Error(), treeB) {
		t.Errorf("refusal must name the holder %s and its tree %s: %v", inEpic2.ID, treeB, err)
	}

	// From a free tree the same story engages: the tree, not the story, was the obstacle.
	chdir(t, treeD)
	var got workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": extra.ID, "status": "plan"}), &got)
	if got.Status != "plan" {
		t.Fatalf("a free tree must admit it: %q", got.Status)
	}
}

// TestWorktreeSeatCreatePathAdmitsUnrelated (sty_69ec7fbc AC1): the create path
// runs the lease probe AND the derived committed-status scan; under worktree both
// must admit an unrelated story created straight into an engaging state from a
// second tree, and the probe must still refuse the occupied tree.
func TestWorktreeSeatCreatePathAdmitsUnrelated(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelWorktree)

	epic := createChild(t, "Epic", "")
	childA := createChild(t, "Child A", epic.ID)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": childA.ID, "status": "plan"}), &childA)

	chdir(t, treeB)
	var other workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Unrelated", "category": "feature", "status": "plan",
	}), &other)
	if other.Status != "plan" {
		t.Fatalf("unrelated create-into-engaging from a free tree must be admitted: %q", other.Status)
	}
	_, err := dispatchRaw(t, "story-create", map[string]any{
		"title": "Same tree", "category": "feature", "status": "plan",
	})
	if err == nil || !errors.Is(err, lease.ErrTreeConflict) {
		t.Fatalf("create from an occupied tree must be a tree conflict: %v", err)
	}
}

// TestWorktreeDerivedScanAdmitsDifferentKeys (sty_69ec7fbc): with no lease row at
// all, the committed-status scan alone must apply the same co-hold rule — it
// admits stories under different keys and refuses a story that shares an
// exclusive key.
func TestWorktreeDerivedScanAdmitsDifferentKeys(t *testing.T) {
	withWiring(t)
	wireWithWorkflows(t, engageWaveWFLine("sequential = true\n"))
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelWorktree}})

	epic, child := engageWaveEpic(t)
	a, b := child(), child()
	stranger := mkEpicStory(t, map[string]any{"category": "fix"})
	other := mkEpicStory(t, map[string]any{"category": "fix"})

	// Commit a and the stranger as engaged WITHOUT leases (a racing writer).
	for _, id := range []string{a.ID, stranger.ID} {
		if err := verb.ForceStatus(context.Background(), id, "plan"); err != nil {
			t.Fatal(err)
		}
	}
	if err := verb.RefuseSecondEngagingStoryDerived(context.Background(), "", "plan", other); err != nil {
		t.Fatalf("an unrelated story must be admitted beside differently-keyed holders: %v", err)
	}
	err := verb.RefuseSecondEngagingStoryDerived(context.Background(), "", "plan", b)
	if err == nil {
		t.Fatalf("a sibling of a sequential epic must be refused while %s holds it", a.ID)
	}
	if !strings.Contains(err.Error(), a.ID) || !strings.Contains(err.Error(), epic.ID) {
		t.Errorf("refusal must name the holder %s and its epic %s: %v", a.ID, epic.ID, err)
	}
}

// TestSeatTagOnlySiblingsShareKey (sty_69ec7fbc AC2, closes sty_9e3520c2): two
// children that belong to an epic by its epic:<theme> tag ALONE (no parent_id)
// resolve to the epic's key, under epic and under worktree, exactly as the wave
// and readiness place them — and engage together from distinct trees.
func TestSeatTagOnlySiblingsShareKey(t *testing.T) {
	for _, mode := range []string{config.ParallelEpic, config.ParallelWorktree} {
		t.Run(mode, func(t *testing.T) {
			treeA, treeB := twoWorktrees(t)
			chdir(t, treeA)
			wireEngageWaveWith(t, engageWaveWFLine(""))
			verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: mode}})

			epic := waveEpic(t)
			a := waveChild(t)
			b := waveChild(t)
			for _, c := range []workitem.Item{a, b} {
				if c.ParentID != "" {
					t.Fatalf("fixture must be tag-only, %s has parent %q", c.ID, c.ParentID)
				}
				if got := verb.SeatKeyFor(context.Background(), c); got != epic.ID {
					t.Errorf("tag-only child %s key = %q, want the epic %s", c.ID, got, epic.ID)
				}
			}
			if err := setStatus(t, a.ID, "plan"); err != nil {
				t.Fatalf("first tag-only sibling: %v", err)
			}
			chdir(t, treeB)
			if err := setStatus(t, b.ID, "plan"); err != nil {
				t.Fatalf("tag-only sibling from a distinct tree must engage under %s: %v", mode, err)
			}
			if got := strings.Count(string(mustSeatList(t)), `"seat_key":"`+epic.ID+`"`); got != 2 {
				t.Errorf("both siblings must list under the epic key, got %d", got)
			}
		})
	}
}

// TestSeatNonEpicParentKeepsParentKey (sty_69ec7fbc AC2): a parent that is not an
// epic container still keys its children by parent_id, as before membership was
// consulted.
func TestSeatNonEpicParentKeepsParentKey(t *testing.T) {
	for _, mode := range []string{config.ParallelEpic, config.ParallelWorktree} {
		t.Run(mode, func(t *testing.T) {
			wireEpicMode(t, mode)
			parent := createChild(t, "Plain parent", "")
			a := createChild(t, "A", parent.ID)
			b := createChild(t, "B", parent.ID)
			for _, c := range []workitem.Item{a, b} {
				if got := verb.SeatKeyFor(context.Background(), c); got != parent.ID {
					t.Errorf("child %s key = %q, want parent %s", c.ID, got, parent.ID)
				}
			}
		})
	}
}

// TestSeatPolicyByMode (sty_69ec7fbc AC1/AC2): the flags each mode hands the
// lease. A sequential epic's children are Exclusive under worktree and fall back
// to a singleton under epic; none never scopes by tree.
func TestSeatPolicyByMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		line      string
		mode      string
		wantTree  bool
		wantExcl  bool
		keyIsEpic bool
	}{
		{"none", "", config.ParallelNone, false, false, false},
		{"epic parallel", "", config.ParallelEpic, false, false, true},
		{"epic sequential", "sequential = true\n", config.ParallelEpic, false, false, false},
		{"worktree parallel", "", config.ParallelWorktree, true, false, true},
		{"worktree sequential", "sequential = true\n", config.ParallelWorktree, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wireEngageWaveWith(t, engageWaveWFLine(tc.line))
			verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: tc.mode}})
			epic, child := engageWaveEpic(t)
			c := child()
			p := verb.SeatPolicyFor(context.Background(), c)
			want := c.ID
			if tc.keyIsEpic {
				want = epic.ID
			}
			if p.Key != want || p.TreeScoped != tc.wantTree || p.Exclusive != tc.wantExcl {
				t.Errorf("policy = %+v, want key %s tree=%v exclusive=%v", p, want, tc.wantTree, tc.wantExcl)
			}
		})
	}
}

// TestSeatSequentialEpicAdmitsOne (sty_69ec7fbc AC2): the children of a
// sequential epic go one at a time under worktree while an unrelated story still
// co-holds from its own tree. The sibling is refused by the SEAT (not the tree):
// it is offered a free tree.
func TestSeatSequentialEpicAdmitsOne(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	treeC := addWorktree(t, treeA, "tree-c")
	chdir(t, treeA)
	wireEngageWaveWith(t, engageWaveWFLine("sequential = true\n"))
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelWorktree}})

	_, child := engageWaveEpic(t)
	a := child()
	if err := setStatus(t, a.ID, "plan"); err != nil {
		t.Fatalf("the sole child must engage: %v", err)
	}
	b := child() // a second, dependency-free child makes the wave wide

	// Sibling create-into-engaging from a free tree: the sequential seat refuses it.
	chdir(t, treeB)
	_, err := dispatchRaw(t, "story-create", map[string]any{
		"title": "sibling", "category": "fix", "status": "plan", "parent_id": b.ParentID,
		"tags": []string{"epic:w"},
	})
	if err == nil {
		t.Fatal("a second child of a sequential epic must be refused while the first holds the seat")
	}
	if errors.Is(err, lease.ErrTreeConflict) {
		t.Errorf("tree %s is free: the refusal must be the seat's, not the tree's: %v", treeB, err)
	}

	// An unrelated story is not affected by the sequential epic.
	chdir(t, treeC)
	var lone workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "unrelated", "category": "fix", "status": "plan",
	}), &lone)
	if lone.Status != "plan" {
		t.Fatalf("an unrelated story must co-hold beside a sequential epic's child: %q", lone.Status)
	}
}
