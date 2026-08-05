//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs a git command in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// gitInitRepo makes dir a git repo with one commit — the precondition for a
// second working tree.
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init")
	gitIn(t, dir, "config", "user.email", "t@t")
	gitIn(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "seed.txt")
	gitIn(t, dir, "commit", "-m", "seed")
}

// addWorktree commits the repo's substrate (a real repo commits .satelle, which
// is how a second working tree is governed at all) and adds a second tree beside
// it. Both trees share one git common dir — which is why they already share one
// satelle runtime plane and one engagement seat, with no new plumbing.
func addWorktree(t *testing.T, dir string) string {
	t.Helper()
	// Only .satelle — the governing substrate. Deliberately NOT .claude: its
	// settings carry this tree's absolute hook paths, and a second tree that
	// inherited them would fail the scaffolding-staleness check instead of
	// exercising the seat.
	gitIn(t, dir, "add", "-f", ".satelle")
	gitIn(t, dir, "commit", "-m", "substrate")
	second := filepath.Join(t.TempDir(), "tree-b")
	gitIn(t, dir, "worktree", "add", "-b", "sibling", second)
	return second
}

// setEngagementMode appends an [engagement] section to a repo's satelle.toml.
func setEngagementMode(t *testing.T, repo, mode string) {
	t.Helper()
	path := filepath.Join(repo, ".satelle", "satelle.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(b) + "\n[engagement]\nparallel = \"" + mode + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSeatParallelEpicAcrossWorktrees (sty_c098dc2d AC2/AC3/AC5) drives the real
// binary end to end: with parallel = "epic", two children of one parent engage
// concurrently from two git working trees; a story under another parent is
// refused naming the holding parent; a second engagement from an already-leased
// tree is refused; and `story seat` lists both sub-leases under the shared key.
func TestSeatParallelEpicAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	mustRun(t, testBin, repo, "init")
	// A reviewer-free spine keeps the case hermetic: seat arbitration is what is
	// under test, not the gates that would otherwise dispatch an agent per edge.
	writeSpineFixture(t, repo, "", "", "", "in_progress|executor|||", "done||||")
	mustRun(t, testBin, repo, "reindex")
	setEngagementMode(t, repo, "epic")
	treeB := addWorktree(t, repo)

	newStory := func(title, parent string) string {
		args := []string{"story", "create", "--title", title,
			"--body", "goal for " + title, "--acceptance", "1. engages", "--category", "chore"}
		if parent != "" {
			args = append(args, "--parent", parent)
		}
		id := extractID(mustRun(t, testBin, repo, args...), "sty_")
		if id == "" {
			t.Fatalf("no story id for %s", title)
		}
		return id
	}
	epic := newStory("Container", "")
	otherEpic := newStory("Other container", "")
	childA := newStory("Child A", epic)
	childB := newStory("Child B", epic)
	stranger := newStory("Stranger", otherEpic)

	// First sibling engages from the primary tree.
	mustRun(t, testBin, repo, "story", "set", childA, "--status", "in_progress")

	// A second engagement from the SAME tree is refused — one tree, one lease.
	out, err := run(t, testBin, repo, "story", "set", childB, "--status", "in_progress")
	if err == nil {
		t.Fatalf("sibling from the occupied tree must be refused:\n%s", out)
	}
	if !strings.Contains(out, "working tree") {
		t.Errorf("refusal should name the tree rule:\n%s", out)
	}

	// From the SECOND working tree the same sibling is admitted.
	mustRun(t, testBin, treeB, "story", "set", childB, "--status", "in_progress")

	// A story under a different parent is refused, and told which parent holds it.
	out, err = run(t, testBin, treeB, "story", "set", stranger, "--status", "in_progress")
	if err == nil {
		t.Fatalf("story under a different parent must be refused:\n%s", out)
	}
	if !strings.Contains(out, epic) {
		t.Errorf("refusal should name the holding parent %s:\n%s", epic, out)
	}

	// Both sub-leases are listed, under the shared key; the parent holds none.
	seat := mustRun(t, testBin, repo, "story", "seat")
	for _, id := range []string{childA, childB} {
		if !strings.Contains(seat, id) {
			t.Fatalf("seat listing missing sub-lease %s:\n%s", id, seat)
		}
	}
	if strings.Count(seat, `"seat_key": "`+epic+`"`) != 2 {
		t.Errorf("both sub-leases should list under the parent key:\n%s", seat)
	}
	if strings.Contains(seat, `"id": "`+epic+`"`) {
		t.Errorf("the parent must never hold a lease of its own:\n%s", seat)
	}

	// story diff is anchored: from the tree it was engaged in it works; from the
	// other tree it refuses and names where to run it.
	if out, err := run(t, testBin, repo, "story", "diff", childA); err != nil {
		t.Fatalf("diff from the engaged tree must work: %v\n%s", err, out)
	}
	out, err = run(t, testBin, treeB, "story", "diff", childA)
	if err == nil {
		t.Fatalf("diff from a foreign tree must be refused:\n%s", out)
	}
	if !strings.Contains(out, "engaged from") {
		t.Errorf("diff refusal should name the recorded tree:\n%s", out)
	}
}

// TestSeatParallelDefaultAndValidation (sty_c098dc2d AC1/AC6): with no
// [engagement] section a sibling is refused exactly as any second story is; and
// an unsupported mode is refused at config load, naming the supported ones.
func TestSeatParallelDefaultAndValidation(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	mustRun(t, testBin, repo, "init")
	writeSpineFixture(t, repo, "", "", "", "in_progress|executor|||", "done||||")
	mustRun(t, testBin, repo, "reindex")
	treeB := addWorktree(t, repo)

	newStory := func(title, parent string) string {
		args := []string{"story", "create", "--title", title,
			"--body", "goal for " + title, "--acceptance", "1. engages", "--category", "chore"}
		if parent != "" {
			args = append(args, "--parent", parent)
		}
		return extractID(mustRun(t, testBin, repo, args...), "sty_")
	}
	epic := newStory("Container", "")
	childA := newStory("Child A", epic)
	childB := newStory("Child B", epic)

	mustRun(t, testBin, repo, "story", "set", childA, "--status", "in_progress")
	// Default mode: siblings buy nothing, even from a distinct working tree.
	out, err := run(t, testBin, treeB, "story", "set", childB, "--status", "in_progress")
	if err == nil {
		t.Fatalf("default mode must refuse a sibling:\n%s", out)
	}
	if !strings.Contains(out, "one performing story") {
		t.Errorf("default refusal wording changed:\n%s", out)
	}

	// An unsupported mode fails the repo closed at load, naming both modes.
	setEngagementMode(t, repo, "all")
	out, err = run(t, testBin, repo, "story", "list")
	if err == nil {
		t.Fatalf("unsupported mode must be refused at load:\n%s", out)
	}
	for _, want := range []string{"none", "epic"} {
		if !strings.Contains(out, want) {
			t.Errorf("load refusal should name mode %q:\n%s", want, out)
		}
	}
}
