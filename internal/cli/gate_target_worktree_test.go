package cli

// sty_42231b74, sty_9c782f0e — one session may drive several stories, each
// engaged from its own worktree (one per epic child). An edit, commit or
// substrate-lock check is attributed by TREE: the story engaged from the tree the
// event targets (its latest working_on row names that tree), else the story
// engaged from the session's own tree, else none — never by the order the store
// lists the stories in. These tests run every attribution in both orders.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const multiTreeSession = "sess-multi"

// multiTree is a repo (the session's anchor tree, also its cwd tree) plus a second
// linked-worktree-shaped tree, each with one story engaged from it by ONE session.
type multiTree struct {
	repo, wtB string
	a, b      workitem.Item
}

// writeGitMarker makes dir a git-tree root for gitRootOf. A linked worktree's
// .git is a regular file, so a file is the faithful marker for both trees.
func writeGitMarker(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// multiTreeRepo engages A from the anchor tree and B from wtB under
// multiTreeSession. aFirst picks which is engaged first. The
// session's own tree is the anchor. gateExtra is appended to the config.
func multiTreeRepo(t *testing.T, aStatus, aCategory, bStatus string, aFirst bool, gateExtra string) multiTree {
	t.Helper()
	repo := editStateRepo(t, aStatus, aStatus, false)
	// B's tree must lie outside every temp root: a write to an out-of-repo temp
	// path is exempt before any story is judged, which would make an edit into B
	// pass whichever story it was attributed to.
	wtB, err := os.MkdirTemp("/var/tmp", "satelle-multitree-")
	if err != nil {
		t.Skipf("cannot place a linked tree outside the temp roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wtB) })
	writeGitMarker(t, repo)
	writeGitMarker(t, wtB)
	stubWorktree(t, repo)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(lockExemptToml+gateExtra), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	items, err := db.Stories.List(ctx, workitem.ListFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("stories: %v n=%d", err, len(items))
	}
	a := items[0]
	if a, err = db.Stories.Update(ctx, a.ID, workitem.UpdateInput{Category: &aCategory}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	b, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "sibling", Body: "goal", AcceptanceCriteria: "1. ok",
		Status: bStatus, Category: "feature",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	type engaged struct {
		it     workitem.Item
		status string
		tree   string
	}
	order := []engaged{{a, aStatus, repo}, {b, bStatus, wtB}}
	if !aFirst {
		order[0], order[1] = order[1], order[0]
	}
	for _, e := range order {
		recordEngagementOn(t, db, e.it.ID, e.status, e.tree, multiTreeSession)
		time.Sleep(5 * time.Millisecond) // time-subject: distinct working_on times, the engagement order is the recording order
	}
	return multiTree{repo: repo, wtB: wtB, a: a, b: b}
}

// multiTreeTwoElsewhere engages two stories from two linked trees neither of which is
// the session's own tree (the anchor), same session id.
func multiTreeTwoElsewhere(t *testing.T) (repo, wtX, wtY string, x, y workitem.Item) {
	t.Helper()
	return multiTreeTwoElsewhereOrdered(t, false)
}

// multiTreeTwoElsewhereOrdered is multiTreeTwoElsewhere with the store order
// chosen: xFirst lists X (at plan, in wtX) before Y (at in_progress, in wtY).
// The later engagement is the later working_on row.
func multiTreeTwoElsewhereOrdered(t *testing.T, xFirst bool) (repo, wtX, wtY string, x, y workitem.Item) {
	t.Helper()
	f := multiTreeRepo(t, "plan", "feature", "in_progress", true, "")
	// Re-engage A from a tree of its own: nothing is then engaged from the anchor.
	wtX = t.TempDir()
	writeGitMarker(t, wtX)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	recordEngagementOn(t, db, f.a.ID, "plan", wtX, multiTreeSession)
	if xFirst {
		// Re-engage Y after X so X is the earlier engagement.
		time.Sleep(5 * time.Millisecond) // time-subject: distinct working_on times, Y is re-engaged strictly after X
		recordEngagementOn(t, db, f.b.ID, "in_progress", f.wtB, multiTreeSession)
	}
	return f.repo, wtX, f.wtB, f.a, f.b
}

func multiTreeEvent(tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{
		"session_id": multiTreeSession, "transcript_path": "/x/transcript.jsonl",
		"tool_name": tool, "tool_input": input,
	})
	return string(b)
}

func multiTreeEdit(path string) string {
	return multiTreeEvent("Edit", map[string]any{"file_path": path, "old_string": "a", "new_string": "b"})
}

func multiTreeBash(command string) string {
	return multiTreeEvent("Bash", map[string]any{"command": command})
}

func bothOrders(t *testing.T, run func(t *testing.T, aFirst bool)) {
	t.Helper()
	for _, aFirst := range []bool{true, false} {
		name := "anchor story engaged second"
		if aFirst {
			name = "anchor story engaged first"
		}
		t.Run(name, func(t *testing.T) { run(t, aFirst) })
	}
}

// AC1: an edit inside the anchor tree is judged against the anchor tree's story. When
// that is the substrate story, the substrate lock passes — even though the
// session's other story (a product story at plan) would refuse it.
func TestMultiTreeSubstrateLockJudgedAgainstAnchorStory(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "substrate", "plan", aFirst, "")
		target := filepath.Join(f.repo, ".satelle", "satelle.toml")
		if out, err := runRootIn(t, multiTreeEdit(target), "hook", "gate"); err != nil {
			t.Fatalf("the substrate story holds the anchor tree's story, so the lock must pass: %v\n%s", err, out)
		}
	})
}

// AC1: the anchor story's OWN refusal applies when it sits at plan, though the
// session's other story is at an executor state that would allow the edit.
func TestMultiTreeEditJudgedAgainstAnchorStoryPlanRefusal(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, "")
		out, err := runRootIn(t, multiTreeEdit(filepath.Join(f.repo, "internal", "foo.go")), "hook", "gate")
		if err == nil {
			t.Fatalf("the anchor story is at plan, so the edit must be refused:\n%s", out)
		}
		reason := denyReasonOf(t, out)
		if !strings.Contains(reason, f.a.ID) && !strings.Contains(reason, `"plan"`) {
			t.Errorf("the refusal must be the plan story's: %s", reason)
		}
		if strings.Contains(reason, f.b.ID) {
			t.Errorf("the refusal must not be attributed to the sibling story %s: %s", f.b.ID, reason)
		}
	})
}

// AC1: a commit from the anchor tree is judged against the anchor tree's story.
func TestMultiTreeCommitgateJudgedAgainstAnchorStory(t *testing.T) {
	const gate = "\n[gate.command_allow]\ncommit = [\"in_progress\"]\n"
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, gate)
		if out, err := runRootIn(t, multiTreeBash("git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("the anchor story is at in_progress, so the commit is allowed: %v\n%s", err, out)
		}
		_ = f
	})
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, gate)
		out, err := runRootIn(t, multiTreeBash("git commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("the anchor story is at plan, so the commit must be refused:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, `"plan"`) {
			t.Errorf("the refusal must name the anchor story's status: %s", reason)
		}
		_ = f
	})
}

// AC1: with the fence opted out, an edit into B's tree is judged against B.
func TestMultiTreeEditIntoOtherTreeJudgedAgainstItsStory(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, "allow_outside_tree_edits = true\n")
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(f.wtB, "foo.go")), "hook", "gate"); err != nil {
			t.Fatalf("B is at in_progress and holds the target tree, so the edit is allowed: %v\n%s", err, out)
		}
		// The anchor tree is still A's: plan refuses.
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(f.repo, "internal", "foo.go")), "hook", "gate"); err == nil {
			t.Fatalf("an edit in the anchor tree is still judged against A (plan):\n%s", out)
		}
	})
}

// A target in no git tree (scratch) falls back to the session's own tree; the
// refusal is the anchor tree's story's, whichever order they were engaged in.
func TestMultiTreeNonRepoTargetFallsBackToSessionTree(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, "")
		info, engaged, _, err := resolveEngagementsFor(multiTreeSession, treeOf("/nonexistent-scratch/x.md"))
		if err != nil || !engaged || info.ItemID != f.a.ID {
			t.Fatalf("scratch target must attribute to the session tree's story %s, got %q engaged=%v err=%v", f.a.ID, info.ItemID, engaged, err)
		}
	})
}

// AC2: with no target the session's own tree decides, whichever order the
// stories were engaged in — the rule every caller (fix claim, story rework,
// status line, stopcheck, substrate status, SessionStart) inherits.
func TestResolveEngagementsNoTargetPicksSessionTree(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, "")
		for _, tc := range []struct {
			tree string
			want string
		}{{f.repo, f.a.ID}, {f.wtB, f.b.ID}} {
			stubWorktree(t, tc.tree)
			info, engaged, _, err := resolveEngagements(multiTreeSession)
			if err != nil || !engaged || info.ItemID != tc.want || !info.Mine {
				t.Fatalf("session tree %s: got %q engaged=%v mine=%v err=%v, want %s", tc.tree, info.ItemID, engaged, info.Mine, err, tc.want)
			}
		}
	})
}

// pickEngagementFor is the one chooser: the story engaged from the target tree
// (the session's own tree when there is no target), else none. Several engaged
// from one tree pick the latest engagement.
func TestPickEngagementForChoosesByTree(t *testing.T) {
	now := time.Now().UTC()
	x := engagement{ItemID: "sty_x", SessionID: "sess", Worktree: "/w/x", EngagedAt: now}
	y := engagement{ItemID: "sty_y", SessionID: "sess", Worktree: "/w/y", EngagedAt: now}
	other := engagement{ItemID: "sty_o", SessionID: "other", Worktree: "/w/o", EngagedAt: now}
	for _, live := range [][]engagement{{x, y, other}, {other, y, x}, {y, x}} {
		stubWorktree(t, "/w/y")
		if got, ok := pickEngagementFor(live, "/w/x"); !ok || got.ItemID != "sty_x" {
			t.Errorf("target tree /w/x: got %q ok=%v", got.ItemID, ok)
		}
		if got, ok := pickEngagementFor(live, ""); !ok || got.ItemID != "sty_y" {
			t.Errorf("no target, session tree /w/y: got %q ok=%v", got.ItemID, ok)
		}
		// A target tree nothing was engaged from picks none.
		if got, ok := pickEngagementFor(live, "/w/elsewhere"); ok {
			t.Errorf("unmatched target tree must pick none, got %q", got.ItemID)
		}
	}
	// Two engaged from one tree: the latest engagement wins, whatever the order.
	older := engagement{ItemID: "sty_old", Worktree: "/w/x", EngagedAt: now.Add(-time.Hour)}
	for _, live := range [][]engagement{{older, x}, {x, older}} {
		if got, _ := pickEngagementFor(live, "/w/x"); got.ItemID != "sty_x" {
			t.Errorf("latest engagement must win, got %q", got.ItemID)
		}
	}
}

func TestTreeOf(t *testing.T) {
	f := multiTreeRepo(t, "plan", "feature", "plan", true, "")
	for _, tc := range []struct{ name, target, want string }{
		{"file in the anchor tree", filepath.Join(f.repo, "internal", "foo.go"), f.repo},
		{"new file in a new dir of the anchor tree", filepath.Join(f.repo, "no", "such", "new.go"), f.repo},
		{"file in a linked tree", filepath.Join(f.wtB, "x.go"), f.wtB},
		{"repo-relative path is the anchor's", "internal/foo.go", f.repo},
		{"scratch outside every tree", "/nonexistent-scratch/x.md", ""},
		{"no target (a Bash event)", "", ""},
	} {
		if got := treeOf(tc.target); got != tc.want {
			t.Errorf("%s: treeOf(%q) = %q, want %q", tc.name, tc.target, got, tc.want)
		}
	}
}

// AC3: the session drives two stories and neither is in the target's tree or the
// session's tree — nothing is picked, and the gate and commitgate name every story
// with its status and worktree.
func TestMultiTreeNoMatchingTreeNamesEveryStory(t *testing.T) {
	repo, wtX, wtY, x, y := multiTreeTwoElsewhere(t)
	check := func(t *testing.T, label, out string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: a session whose stories are all engaged elsewhere must be refused:\n%s", label, out)
		}
		reason := denyReasonOf(t, out)
		for _, want := range []string{x.ID, y.ID, "(plan,", "(in_progress,", wtX, wtY} {
			if !strings.Contains(reason, want) {
				t.Errorf("%s: deny missing %q: %s", label, want, reason)
			}
		}
		if strings.Contains(reason, noEngagedStoryEditReason) {
			t.Errorf("%s: the generic engage-a-story text must not stand in for the performing list: %s", label, reason)
		}
	}
	out, err := runRootIn(t, multiTreeEdit(filepath.Join(repo, "internal", "foo.go")), "hook", "gate")
	check(t, "gate", out, err)
	out, err = runRootIn(t, multiTreeBash("echo x > "+filepath.Join(repo, "internal", "foo.go")), "hook", "commitgate")
	check(t, "commitgate mutation", out, err)
	out, err = runRootIn(t, multiTreeBash("git commit -m x"), "hook", "commitgate")
	check(t, "commitgate commit", out, err)
}

const commitInProgressOnly = "\n[gate.command_allow]\ncommit = [\"in_progress\"]\n"

// sty_3a9b06fe AC1: a commit that a leading cd moved into a worktree is judged
// against that worktree's story, whichever order they were engaged in.
func TestMultiTreeCommitFromWorktreeAttributedToItsStory(t *testing.T) {
	// The session's own tree has none of its stories engaged from it: such a commit used to be
	// refused as unattributable.
	bothOrders(t, func(t *testing.T, xFirst bool) {
		_, _, wtY, _, _ := multiTreeTwoElsewhereOrdered(t, xFirst)
		if out, err := runRootIn(t, multiTreeBash("cd "+wtY+" && git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("the commit runs in Y's worktree, so it is attributed to Y and allowed: %v\n%s", err, out)
		}
	})
	// The anchor's story is at plan, B's at in_progress: the commit is B's to make.
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, commitInProgressOnly)
		if out, err := runRootIn(t, multiTreeBash("cd "+f.wtB+" && git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("B is at in_progress and holds the worktree the commit runs in: %v\n%s", err, out)
		}
	})
}

// sty_3a9b06fe AC2: the moved commit is decided by the story's own state under the
// same rules as any commit — no new check.
func TestMultiTreeMovedCommitDecidedByItsStoryState(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, commitInProgressOnly)
		out, err := runRootIn(t, multiTreeBash("cd "+f.wtB+" && git commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("B is at plan, so the commit in B's worktree must be refused though A would allow it:\n%s", out)
		}
		reason := denyReasonOf(t, out)
		if !strings.Contains(reason, "plan") || !strings.Contains(reason, f.b.ID) {
			t.Errorf("the refusal must name B's status and story: %s", reason)
		}
		if strings.Contains(reason, f.a.ID) {
			t.Errorf("the refusal must not be attributed to A (%s): %s", f.a.ID, reason)
		}
	})
	// Without [gate.command_allow] the engaged story is all a commit needs.
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, "")
		if out, err := runRootIn(t, multiTreeBash("cd "+f.wtB+" && git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("no command_allow: an engaged story may commit whatever its status: %v\n%s", err, out)
		}
	})
}

// sty_3a9b06fe AC3: a commit nothing moved is attributed to the story in the hook's
// own tree (sessionWorktree), not to the anchor's.
func TestMultiTreePlainCommitKeepsHookTreeStory(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		// The hook runs in B's tree while the anchor (config root) holds A.
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, commitInProgressOnly)
		stubWorktree(t, f.wtB)
		out, err := runRootIn(t, multiTreeBash("git commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("the hook runs in B's tree and B is at plan, so the commit is refused:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, f.b.ID) {
			t.Errorf("the refusal must be B's: %s", reason)
		}
	})
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, commitInProgressOnly)
		stubWorktree(t, f.wtB)
		if out, err := runRootIn(t, multiTreeBash("git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("the hook runs in B's tree and B is at in_progress: %v\n%s", err, out)
		}
	})
}

// sty_3a9b06fe AC3: a command that runs in no engaged tree is refused as before,
// moved or not.
func TestMultiTreeCommitInUnengagedTreeStillRefused(t *testing.T) {
	_, _, _, _, _ = multiTreeTwoElsewhere(t)
	for _, command := range []string{"git commit -m x", "cd " + t.TempDir() + " && git commit -m x"} {
		out, err := runRootIn(t, multiTreeBash(command), "hook", "commitgate")
		if err == nil {
			t.Fatalf("%q runs in no engaged tree and must be refused:\n%s", command, out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, "no performing story was engaged from this tree") {
			t.Errorf("%q: want the existing unattributed refusal, got: %s", command, reason)
		}
	}
}

// sty_3a9b06fe AC4: the foreign-tree fence is untouched. git -C into another
// tree is refused by default; with the fence opted out it is attributed to that
// tree's story by the same rule as a cd.
func TestMultiTreeGitDashCFenceAndAttribution(t *testing.T) {
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, commitInProgressOnly)
		out, err := runRootIn(t, multiTreeBash("git -C "+f.wtB+" commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("git -C into another tree is still refused by containment:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, "another repo's tree") {
			t.Errorf("want the containment refusal, got: %s", reason)
		}
	})
	const optOut = "allow_outside_tree_edits = true\n" + commitInProgressOnly
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "plan", "feature", "in_progress", aFirst, optOut)
		if out, err := runRootIn(t, multiTreeBash("git -C "+f.wtB+" commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("fence opted out and B (in_progress) holds the tree the commit runs in: %v\n%s", err, out)
		}
	})
	bothOrders(t, func(t *testing.T, aFirst bool) {
		f := multiTreeRepo(t, "in_progress", "feature", "plan", aFirst, optOut)
		out, err := runRootIn(t, multiTreeBash("git -C "+f.wtB+" commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("fence opted out, but B is at plan, so the commit in B's tree is refused:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, f.b.ID) {
			t.Errorf("the refusal must be B's: %s", reason)
		}
	})
}

// sty_68a4d071 — real linked worktrees. The fixtures above fake a tree with a
// .git marker; these run `git worktree add`, so a branch really is checked out in a
// worktree and the hook, running in the main tree under the default fence, has to
// attribute by the tree an event targets.

func resolvedPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// linkedTrees is the main tree (repo, which nothing is engaged from and where the hook
// runs) and two linked worktrees, wtX on feat/x and wtY on feat/y, each engaging one
// story under multiTreeSession.
type linkedTrees struct {
	repo, wtX, wtY string
	x, y           workitem.Item
}

// multiTreeLinked engages X (xStatus, from wtX) and Y (yStatus, from wtY). xFirst
// picks which is engaged first. yInFlight leaves a transition on Y open.
func multiTreeLinked(t *testing.T, xStatus, yStatus string, xFirst, yInFlight bool, gateExtra string) linkedTrees {
	t.Helper()
	f := multiTreeRepo(t, xStatus, "feature", yStatus, true, gateExtra)
	// multiTreeRepo's fake markers give way to a real repository and real worktrees.
	_ = os.Remove(filepath.Join(f.repo, ".git"))
	if err := os.RemoveAll(f.wtB); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "init", "-q", "-b", "main")
	gitIn(t, f.repo, "commit", "-q", "--allow-empty", "-m", "init")
	wtX, err := os.MkdirTemp("/var/tmp", "satelle-linked-x-")
	if err != nil {
		t.Skipf("cannot place a linked tree outside the temp roots: %v", err)
	}
	if err := os.Remove(wtX); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wtX) })
	gitIn(t, f.repo, "worktree", "add", "-q", "-b", "feat/x", wtX)
	gitIn(t, f.repo, "worktree", "add", "-q", "-b", "feat/y", f.wtB)
	wtX, wtY := resolvedPath(t, wtX), resolvedPath(t, f.wtB)

	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	type engaged struct {
		it     workitem.Item
		status string
		tree   string
		flight bool
	}
	order := []engaged{{f.a, xStatus, wtX, false}, {f.b, yStatus, wtY, yInFlight}}
	if !xFirst {
		order[0], order[1] = order[1], order[0]
	}
	for _, e := range order {
		recordEngagementOn(t, db, e.it.ID, e.status, e.tree, multiTreeSession)
		if e.flight {
			startTransitionOn(t, db, e.it.ID, e.status)
		}
		time.Sleep(5 * time.Millisecond) // time-subject: distinct working_on times, the engagement order is the recording order
	}
	return linkedTrees{repo: f.repo, wtX: wtX, wtY: wtY, x: f.a, y: f.b}
}

const pushInProgressOnly = "\n[gate.command_allow]\ncommit = [\"in_progress\"]\npush = [\"in_progress\"]\n"

// TestGateDecidesFromTargetWorktree (sty_9c782f0e AC5), was AC1 of sty_68a4d071:
// from the main tree, with the default fence, an Edit into each linked
// worktree is judged against that worktree's story.
func TestGateDecidesFromTargetWorktree(t *testing.T) {
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "plan", "in_progress", xFirst, false, "")
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(l.wtY, "a.go")), "hook", "gate"); err != nil {
			t.Fatalf("Y is at in_progress and holds the target tree, so the edit is allowed: %v\n%s", err, out)
		}
		out, err := runRootIn(t, multiTreeEdit(filepath.Join(l.wtX, "a.go")), "hook", "gate")
		if err == nil {
			t.Fatalf("X is at plan and holds the target tree, so the edit is refused:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, l.x.ID) || strings.Contains(reason, l.y.ID) {
			t.Errorf("the refusal must be X's alone: %s", reason)
		}
	})
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "in_progress", "plan", xFirst, false, "")
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(l.wtX, "a.go")), "hook", "gate"); err != nil {
			t.Fatalf("X is at in_progress and holds the target tree, so the edit is allowed: %v\n%s", err, out)
		}
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(l.wtY, "a.go")), "hook", "gate"); err == nil {
			t.Fatalf("Y is at plan and holds the target tree, so the edit is refused:\n%s", out)
		}
	})
}

// AC1: a commit a cd moved into a linked worktree is judged against its story.
func TestMultiTreeLinkedCommitJudgedAgainstWorktreeStory(t *testing.T) {
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "plan", "in_progress", xFirst, false, pushInProgressOnly)
		if out, err := runRootIn(t, multiTreeBash("cd "+l.wtY+" && git commit -m x"), "hook", "commitgate"); err != nil {
			t.Fatalf("Y is at in_progress and the commit runs in its worktree: %v\n%s", err, out)
		}
		out, err := runRootIn(t, multiTreeBash("cd "+l.wtX+" && git commit -m x"), "hook", "commitgate")
		if err == nil {
			t.Fatalf("X is at plan and the commit runs in its worktree:\n%s", out)
		}
		if reason := denyReasonOf(t, out); !strings.Contains(reason, l.x.ID) {
			t.Errorf("the refusal must be X's: %s", reason)
		}
	})
}

// AC1: an unmoved push from the main tree is judged against the story of the
// worktree that has the pushed branch checked out.
func TestMultiTreeLinkedPushAttributedByBranch(t *testing.T) {
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "plan", "in_progress", xFirst, false, pushInProgressOnly)
		for _, command := range []string{"git push origin feat/y", "git push -u origin feat/y:feat/y", "git push origin +refs/heads/feat/y"} {
			if out, err := runRootIn(t, multiTreeBash(command), "hook", "commitgate"); err != nil {
				t.Fatalf("%q: feat/y is checked out in Y's worktree and Y is at in_progress: %v\n%s", command, err, out)
			}
		}
		for _, command := range []string{"git push origin feat/x", "git push -u origin feat/x:feat/x"} {
			out, err := runRootIn(t, multiTreeBash(command), "hook", "commitgate")
			if err == nil {
				t.Fatalf("%q: feat/x is checked out in X's worktree and X is at plan:\n%s", command, out)
			}
			if reason := denyReasonOf(t, out); !strings.Contains(reason, l.x.ID) || strings.Contains(reason, l.y.ID) {
				t.Errorf("%q: the refusal must be X's alone: %s", command, reason)
			}
		}
	})
}

// An explicit cd still beats the branch: a moved push is decided by its directory.
func TestMultiTreeLinkedMovedPushKeepsItsDirectory(t *testing.T) {
	l := multiTreeLinked(t, "plan", "in_progress", true, false, pushInProgressOnly)
	if out, err := runRootIn(t, multiTreeBash("cd "+l.wtY+" && git push origin feat/x"), "hook", "commitgate"); err != nil {
		t.Fatalf("the push runs in Y's worktree, which decides it: %v\n%s", err, out)
	}
}

// AC2: what targets no engaged worktree keeps today's refusal.
func TestMultiTreeLinkedUnattributableStillRefused(t *testing.T) {
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "in_progress", "in_progress", xFirst, false, pushInProgressOnly)
		type event struct{ label, command, edit string }
		for _, ev := range []event{
			{"unmoved commit", "git commit -m x", ""},
			{"push of a branch no worktree has", "git push origin no-such-branch", ""},
			{"push of the main tree's branch", "git push origin main", ""},
			{"bare push", "git push", ""},
			{"push of HEAD", "git push origin HEAD", ""},
			{"push of two trees' branches", "git push origin feat/x feat/y", ""},
			{"unmoved commit then push of an engaged tree's branch", "git commit -m x && git push origin feat/y", ""},
			{"shell mutation in the main tree", "echo x > " + filepath.Join(l.repo, "internal", "foo.go"), ""},
			{"edit in the main tree", "", filepath.Join(l.repo, "internal", "foo.go")},
		} {
			var out string
			var err error
			if ev.edit != "" {
				out, err = runRootIn(t, multiTreeEdit(ev.edit), "hook", "gate")
			} else {
				out, err = runRootIn(t, multiTreeBash(ev.command), "hook", "commitgate")
			}
			if err == nil {
				t.Fatalf("%s must be refused:\n%s", ev.label, out)
			}
			reason := denyReasonOf(t, out)
			for _, want := range []string{"no performing story was engaged from this tree", l.x.ID, l.y.ID} {
				if !strings.Contains(reason, want) {
					t.Errorf("%s: refusal missing %q: %s", ev.label, want, reason)
				}
			}
		}
	})
}

// AC3: scratch paths outside every repo tree stay ungated while the session holds
// two stories and one has a transition in flight.
func TestMultiTreeLinkedScratchAllowedWhileTransitionInFlight(t *testing.T) {
	bothOrders(t, func(t *testing.T, xFirst bool) {
		l := multiTreeLinked(t, "plan", "in_progress", xFirst, true, "")
		scratch := filepath.Join(t.TempDir(), "scratchpad", "brief.md")
		if out, err := runRootIn(t, multiTreeEdit(scratch), "hook", "gate"); err != nil {
			t.Fatalf("an Edit to scratch %s is allowed: %v\n%s", scratch, err, out)
		}
		write := multiTreeEvent("Write", map[string]any{"file_path": scratch, "content": "x"})
		if out, err := runRootIn(t, write, "hook", "gate"); err != nil {
			t.Fatalf("a Write to scratch is allowed: %v\n%s", err, out)
		}
		redirect := multiTreeBash("echo x > " + scratch)
		if out, err := runRootIn(t, redirect, "hook", "gate"); err != nil {
			t.Fatalf("a shell redirect to scratch is allowed by the gate: %v\n%s", err, out)
		}
		if out, err := runRootIn(t, redirect, "hook", "commitgate"); err != nil {
			t.Fatalf("a shell redirect to scratch is allowed by the commitgate: %v\n%s", err, out)
		}
		// The in-flight story still fences its own tree.
		if out, err := runRootIn(t, multiTreeEdit(filepath.Join(l.wtY, "a.go")), "hook", "gate"); err == nil {
			t.Fatalf("an edit into the in-flight story's tree is still refused:\n%s", out)
		}
	})
}

func TestGitPushBranches(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"git push origin feat/x", []string{"refs/heads/feat/x"}},
		{"git push -u origin feat/x", []string{"refs/heads/feat/x"}},
		{"git push --force-with-lease origin feat/x", []string{"refs/heads/feat/x"}},
		{"git push origin feat/x:release", []string{"refs/heads/feat/x"}},
		{"git push origin +feat/x:feat/x", []string{"refs/heads/feat/x"}},
		{"git push origin refs/heads/feat/x", []string{"refs/heads/feat/x"}},
		{"git push -o ci.skip origin feat/x", []string{"refs/heads/feat/x"}},
		{"git push origin a b", []string{"refs/heads/a", "refs/heads/b"}},
		{"git -C /w push origin feat/x", []string{"refs/heads/feat/x"}},
		{"git push origin feat/x && git push origin feat/y", []string{"refs/heads/feat/x", "refs/heads/feat/y"}},
		{"git push origin feat/x && git push", nil},
		{"git commit -m x && git push origin feat/x", nil},
		{"git status && git push origin feat/x", nil},
		{"git push", nil},
		{"git push origin", nil},
		{"git push origin HEAD", nil},
		{"git push origin HEAD:feat/x", nil},
		{"git push --all origin", nil},
		{"git push --tags origin v1", nil},
		{"git push --mirror origin", nil},
		{"git push origin --delete feat/x", nil},
		{"git push origin :feat/x", nil},
		{"git push origin refs/tags/v1", nil},
		{"git commit -m x", nil},
		{"echo git push origin feat/x", nil},
	} {
		if got := gitPushBranches(tc.command); !slices.Equal(got, tc.want) {
			t.Errorf("gitPushBranches(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}
