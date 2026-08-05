package verb_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// twoWorktrees builds a real git repo with a real second working tree — the
// shape the mode exists for. Returns both toplevels; the caller chdirs between
// them. Both trees share one git common dir, which is exactly why they already
// share one satelle DB and one seat (config.RepoKey).
func twoWorktrees(t *testing.T) (treeA, treeB string) {
	t.Helper()
	treeA = t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v in %s: %v\n%s", args, dir, err, out)
		}
	}
	run(treeA, "git", "init")
	run(treeA, "git", "config", "user.email", "t@t")
	run(treeA, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(treeA, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(treeA, "git", "add", "a.txt")
	run(treeA, "git", "commit", "-m", "init")
	// Sibling tree beside the first so neither is nested inside the other.
	treeB = filepath.Join(t.TempDir(), "tree-b")
	run(treeA, "git", "worktree", "add", "-b", "sibling", treeB)
	// git resolves symlinked temp dirs (macOS /var → /private/var); ask git for
	// the canonical toplevel so the test compares what the code compares.
	top := func(dir string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			t.Fatalf("toplevel %s: %v", dir, err)
		}
		return strings.TrimSpace(string(out))
	}
	return top(treeA), top(treeB)
}

// engagementModeTestWF is singleStoryWF's shape under a fresh name so these
// cases are independent of the fixtures the single-occupancy tests pin.
var engagementModeTestWF = routeHalves(
	`["*"]
obligations = ["raised", "planned", "coded", "closed"]
park = { state = "blocked" }
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

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

// wireEpicMode wires the workflow fixture and the epic seat concurrency mode,
// restoring the default afterwards so no other test inherits it.
func wireEpicMode(t *testing.T, mode string) {
	t.Helper()
	wireWithWorkflows(t, engagementModeTestWF)
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: mode}})
	t.Cleanup(verb.ClearEngagementMode)
}

func createChild(t *testing.T, title, parent string) workitem.Item {
	t.Helper()
	req := map[string]any{"title": title, "category": "feature"}
	if parent != "" {
		req["parent_id"] = parent
	}
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", req), &it)
	return it
}

// TestEpicSeatAdmitsSiblingsFromDistinctWorktrees (sty_c098dc2d AC2): two
// children of one parent engage concurrently from two working trees; a story
// under a different parent, and a parentless story, are both refused while they
// hold the seat, and the refusal names the epic that holds it.
func TestEpicSeatAdmitsSiblingsFromDistinctWorktrees(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelEpic)

	epic := createChild(t, "Container", "")
	other := createChild(t, "Other container", "")
	childA := createChild(t, "Child A", epic.ID)
	childB := createChild(t, "Child B", epic.ID)
	stranger := createChild(t, "Stranger", other.ID)
	lone := createChild(t, "Parentless", "")

	json.Unmarshal(call(t, "story-set", map[string]any{"id": childA.ID, "status": "plan"}), &childA)
	if childA.Status != "plan" {
		t.Fatalf("first sibling engage: %q", childA.Status)
	}
	// Second sibling, second tree → admitted. This is the whole point.
	chdir(t, treeB)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": childB.ID, "status": "plan"}), &childB)
	if childB.Status != "plan" {
		t.Fatalf("sibling from a distinct tree must engage: %q", childB.Status)
	}

	// A story under a DIFFERENT parent is refused, and told which epic holds it.
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": stranger.ID, "status": "plan"})
	if err == nil {
		t.Fatal("story under a different epic must be refused")
	}
	if !strings.Contains(err.Error(), epic.ID) {
		t.Errorf("refusal must name the holding epic %s: %v", epic.ID, err)
	}
	if !strings.Contains(err.Error(), "engagement seat") {
		t.Errorf("refusal must keep the seat vocabulary: %v", err)
	}
	// A PARENTLESS story is refused too — it would claim the seat as a singleton.
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": lone.ID, "status": "plan"}); err == nil {
		t.Fatal("parentless story must be refused while an epic holds the seat")
	}

	// The parent is NEVER engaged: it holds the key, not a lease.
	seats := string(mustSeatList(t))
	if strings.Contains(seats, `"id":"`+epic.ID) {
		t.Fatalf("epic must not hold a lease of its own: %s", seats)
	}
	for _, id := range []string{childA.ID, childB.ID} {
		if !strings.Contains(seats, id) {
			t.Fatalf("sub-lease %s missing from seat list: %s", id, seats)
		}
	}
	// Sub-leases carry the epic key, so the listing groups under it (AC5).
	if strings.Count(seats, `"seat_key":"`+epic.ID+`"`) != 2 {
		t.Fatalf("both sub-leases must list under the epic key: %s", seats)
	}
}

// TestEpicSeatSingletonClaimByParentlessFirst (sty_c098dc2d AC2): a parentless
// story engaging FIRST claims the seat as a singleton — equivalent to `none`
// for its duration, so even a would-be sibling pair is refused behind it.
func TestEpicSeatSingletonClaimByParentlessFirst(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelEpic)

	lone := createChild(t, "Parentless first", "")
	epic := createChild(t, "Container", "")
	child := createChild(t, "Child", epic.ID)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": lone.ID, "status": "plan"}), &lone)
	if lone.Status != "plan" {
		t.Fatalf("parentless engage: %q", lone.Status)
	}
	chdir(t, treeB)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": child.ID, "status": "plan"})
	if err == nil {
		t.Fatal("nothing may join a singleton claim, even from another tree")
	}
	if !strings.Contains(err.Error(), "one performing story") {
		t.Errorf("singleton refusal keeps the single-occupancy wording: %v", err)
	}
}

// TestEpicSeatTreeConflict (sty_c098dc2d AC3): a VALID sibling engaged from an
// already-leased tree is refused as a tree conflict — the discriminator is the
// sentinel, not the message text — and the story is left untouched.
func TestEpicSeatTreeConflict(t *testing.T) {
	treeA, _ := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelEpic)

	epic := createChild(t, "Container", "")
	childA := createChild(t, "Child A", epic.ID)
	childB := createChild(t, "Child B", epic.ID)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": childA.ID, "status": "plan"}), &childA)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": childB.ID, "status": "plan"})
	if err == nil {
		t.Fatal("second sibling from the SAME tree must be refused")
	}
	if !errors.Is(err, lease.ErrTreeConflict) {
		t.Fatalf("refusal must carry the tree-conflict sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), treeA) {
		t.Errorf("refusal must name the occupied tree: %v", err)
	}
	var still workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": childB.ID}), &still)
	if still.Status != workitem.StatusBacklog {
		t.Errorf("refused sibling must not have moved: %q", still.Status)
	}
}

// TestEpicSeatCreatePathIsKeyAware (sty_c098dc2d AC5, reader 4): the create path
// runs BOTH the lease probe and the derived committed-status scan. Both must
// apply the same key rule — otherwise a sibling created straight into an
// engaging state clears the probe and is then refused by the scan.
func TestEpicSeatCreatePathIsKeyAware(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelEpic)

	epic := createChild(t, "Container", "")
	other := createChild(t, "Other container", "")
	childA := createChild(t, "Child A", epic.ID)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": childA.ID, "status": "plan"}), &childA)

	chdir(t, treeB)
	var sibling workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Child B", "category": "feature", "parent_id": epic.ID, "status": "plan",
	}), &sibling)
	if sibling.Status != "plan" {
		t.Fatalf("sibling create-into-engaging must be admitted: %q", sibling.Status)
	}
	// A stranger create is still refused — by whichever check answers first.
	_, err := dispatchRaw(t, "story-create", map[string]any{
		"title": "Stranger", "category": "feature", "parent_id": other.ID, "status": "plan",
	})
	if err == nil {
		t.Fatal("create-into-engaging under a different epic must be refused")
	}
	if !strings.Contains(err.Error(), epic.ID) {
		t.Errorf("create refusal must name the holding epic: %v", err)
	}
}

// TestNoneModeRefusesSiblings (sty_c098dc2d AC1): with the default mode, being
// siblings buys nothing — the second child is refused exactly as any second
// story is, with the single-occupancy wording, from any tree.
func TestNoneModeRefusesSiblings(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelNone)

	epic := createChild(t, "Container", "")
	childA := createChild(t, "Child A", epic.ID)
	childB := createChild(t, "Child B", epic.ID)

	json.Unmarshal(call(t, "story-set", map[string]any{"id": childA.ID, "status": "plan"}), &childA)
	chdir(t, treeB)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": childB.ID, "status": "plan"})
	if err == nil {
		t.Fatal("default mode must refuse a sibling")
	}
	if !strings.Contains(err.Error(), "one performing story") {
		t.Errorf("default refusal wording changed: %v", err)
	}
	if errors.Is(err, lease.ErrTreeConflict) {
		t.Errorf("distinct trees under the default mode is still a SEAT refusal: %v", err)
	}
}

// TestStoryDiffAnchoredToLeaseWorktree (sty_c098dc2d AC7): an engaged story's
// diff resolves against the tree it was engaged from. Run there it diffs the
// baseline; run from another tree it refuses and names the recorded path,
// rather than silently attributing that tree's changes to the story.
func TestStoryDiffAnchoredToLeaseWorktree(t *testing.T) {
	treeA, treeB := twoWorktrees(t)
	chdir(t, treeA)
	wireEpicMode(t, config.ParallelEpic)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	epic := createChild(t, "Container", "")
	child := createChild(t, "Child A", epic.ID)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": child.ID, "status": "plan"}), &child)

	// A change in the engaged tree shows up in its own diff.
	if err := os.WriteFile(filepath.Join(treeA, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := verbDiff(t, child.ID)
	if err != nil {
		t.Fatalf("diff from the engaged tree must work: %v", err)
	}
	if !strings.Contains(string(raw), "new.txt") {
		t.Errorf("diff from the engaged tree missed its change: %s", raw)
	}

	// From the OTHER tree it refuses, naming where to run it instead.
	chdir(t, treeB)
	_, err = dispatchRaw(t, "story-diff", map[string]any{"id": child.ID})
	if err == nil {
		t.Fatal("diff from a foreign tree must be refused")
	}
	if !strings.Contains(err.Error(), treeA) {
		t.Errorf("refusal must name the recorded tree: %v", err)
	}
	if !strings.Contains(err.Error(), treeB) {
		t.Errorf("refusal must name the invocation tree: %v", err)
	}
}

func verbDiff(t *testing.T, id string) ([]byte, error) {
	t.Helper()
	return dispatchRaw(t, "story-diff", map[string]any{"id": id})
}

func mustSeatList(t *testing.T) []byte {
	t.Helper()
	out, err := dispatchRaw(t, "story-seat-list", map[string]any{})
	if err != nil {
		t.Fatalf("story-seat-list: %v", err)
	}
	return out
}
