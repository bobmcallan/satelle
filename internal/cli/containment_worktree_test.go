package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sty_bcf837ff: the foreign-tree fence recognises a linked worktree of the
// session's own repository, and does not fence temp roots.

// fenceTempRootsElsewhere points the fence's temp exemption at a directory no
// test repo lives under. Test repos are t.TempDir()s, which the real temp roots
// would exempt, so a foreign-repo case would pass without exercising the fence.
func fenceTempRootsElsewhere(t *testing.T) {
	t.Helper()
	old := containmentTempRoots
	containmentTempRoots = func() []string { return []string{"/nonexistent-satelle-temp-root"} }
	t.Cleanup(func() { containmentTempRoots = old })
}

// gitInitRepo makes a real repo with one commit at dir (created if absent).
func gitInitRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "seed")
	return dir
}

// linkedWorktree adds a linked worktree of main and returns its resolved path.
func linkedWorktree(t *testing.T, main string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "linked")
	gitIn(t, main, "worktree", "add", "-q", "-b", "linked-"+filepath.Base(filepath.Dir(wt)), wt)
	if r, err := filepath.EvalSymlinks(wt); err == nil {
		wt = r
	}
	return wt
}

// AC1: a linked worktree of the session's own repository is not a foreign tree,
// for the Bash predicate and the Edit predicate alike.
func TestForeignTreeTarget_LinkedWorktreeSameRepo(t *testing.T) {
	fenceTempRootsElsewhere(t)
	main := gitInitRepo(t, t.TempDir())
	wt := linkedWorktree(t, main)
	target := filepath.Join(wt, "internal", "cli", "app.go")

	if p, r, ok := foreignTreeTarget(main, []string{target}); ok {
		t.Errorf("linked worktree of the session repo is foreign: path=%q root=%q", p, r)
	}
	t.Setenv("SATELLE_PROJECT_DIR", main)
	if r, foreign := editTargetForeign(target); foreign {
		t.Errorf("editTargetForeign refused a linked worktree of the session repo: root=%q", r)
	}
	// The relation is symmetric: a session anchored in the linked tree may edit main.
	if _, _, ok := foreignTreeTarget(wt, []string{filepath.Join(main, "f.txt")}); ok {
		t.Error("main tree must not be foreign to a session anchored in its linked worktree")
	}
}

// AC1: the fence stepping aside does not waive the ordinary edit gate — an Edit
// in the linked worktree with no engaged story is denied by the engaged-story
// rule, not by the foreign-tree reason.
func TestHookGate_LinkedWorktreeEditStillNeedsAStory(t *testing.T) {
	fenceTempRootsElsewhere(t)
	repo := tempRepo(t)
	t.Chdir(repo)
	t.Setenv("SATELLE_PROJECT_DIR", repo)
	gitInitRepo(t, repo)
	wt := linkedWorktree(t, repo)

	out, err := runRootIn(t, editEvent(filepath.Join(wt, "app.go")), "hook", "gate")
	if err == nil {
		t.Fatalf("an edit with no engaged story must be denied:\n%s", out)
	}
	if strings.Contains(out, "another repo's tree") || strings.Contains(out, "allow_outside_tree_edits") {
		t.Errorf("a linked worktree must not be refused as a foreign tree: %s", out)
	}
	if !strings.Contains(out, "without a performing story") {
		t.Errorf("expected the engaged-story refusal: %s", out)
	}
}

// AC2: a different repository, and a repo whose common dir cannot be resolved,
// are still foreign.
func TestForeignTreeTarget_OtherRepoStillForeign(t *testing.T) {
	fenceTempRootsElsewhere(t)
	main := gitInitRepo(t, t.TempDir())
	other := gitInitRepo(t, t.TempDir())
	target := filepath.Join(other, "f.txt")

	if _, r, ok := foreignTreeTarget(main, []string{target}); !ok || r != filepath.Clean(other) {
		t.Errorf("independent repo must be foreign: ok=%v root=%q", ok, r)
	}
	t.Setenv("SATELLE_PROJECT_DIR", main)
	if r, foreign := editTargetForeign(target); !foreign || r != filepath.Clean(other) {
		t.Errorf("editTargetForeign: foreign=%v root=%q", foreign, r)
	}
	// A linked worktree of the OTHER repo is foreign too.
	if _, _, ok := foreignTreeTarget(main, []string{filepath.Join(linkedWorktree(t, other), "f.txt")}); !ok {
		t.Error("a linked worktree of another repo must be foreign")
	}

	// Fail closed: a .git git cannot read leaves the common dir unresolved.
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nonexistent/satelle-gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := foreignTreeTarget(main, []string{filepath.Join(broken, "x.go")}); !ok {
		t.Error("an unresolvable common dir must be treated as foreign")
	}
	if r, foreign := editTargetForeign(filepath.Join(broken, "x.go")); !foreign {
		t.Errorf("editTargetForeign must fail closed: root=%q", r)
	}
}

// AC3: a git root under the temp roots is not fenced (Bash delete candidate and
// Edit target), but the exemption never covers a foreign repo outside them.
func TestForeignTreeTarget_TempGitRootNotFenced(t *testing.T) {
	base := t.TempDir()
	old := containmentTempRoots
	containmentTempRoots = func() []string { return []string{base} }
	t.Cleanup(func() { containmentTempRoots = old })

	anchor := gitInitRepo(t, t.TempDir()) // sibling of base: outside the temp root
	clone := gitInitRepo(t, filepath.Join(base, "scratchpad", "70-clean"))

	if p, _, ok := foreignTreeTarget(anchor, []string{clone}); ok {
		t.Errorf("a clone under the temp root must not be fenced: %q", p)
	}
	t.Setenv("SATELLE_PROJECT_DIR", anchor)
	if r, foreign := editTargetForeign(filepath.Join(clone, "f.txt")); foreign {
		t.Errorf("an Edit under the temp root must not be fenced: root=%q", r)
	}

	// A foreign repo outside the temp roots is still fenced.
	other := gitInitRepo(t, t.TempDir())
	if _, _, ok := foreignTreeTarget(anchor, []string{filepath.Join(other, "f.txt")}); !ok {
		t.Error("a foreign repo outside the temp roots must stay fenced")
	}
}

// AC1: a Bash mutation into a linked worktree of the session repo is no longer
// fenced, so it must reach the engaged-story rule on both Bash hooks — never
// silently allowed because the target sits outside the anchor.
func TestHookBash_LinkedWorktreeMutationStillNeedsAStory(t *testing.T) {
	fenceTempRootsElsewhere(t)
	repo := tempRepo(t)
	t.Chdir(repo)
	t.Setenv("SATELLE_PROJECT_DIR", repo)
	gitInitRepo(t, repo)
	wt := linkedWorktree(t, repo)

	for _, hook := range []string{"gate", "commitgate"} {
		for _, command := range []string{
			"echo x > " + filepath.Join(wt, "app.go"),
			"sed -i s/a/b/ " + filepath.Join(wt, "app.go"),
			"rm -rf " + filepath.Join(wt, "internal"),
		} {
			out, err := runRootIn(t, bashEvent(command), "hook", hook)
			if err == nil {
				t.Errorf("%s: %q with no engaged story must be denied:\n%s", hook, command, out)
				continue
			}
			if strings.Contains(out, "another repo's tree") || strings.Contains(out, "allow_outside_tree_edits") {
				t.Errorf("%s: %q refused as a foreign tree: %s", hook, command, out)
			}
			if !strings.Contains(out, "without a performing story") {
				t.Errorf("%s: %q expected the engaged-story refusal: %s", hook, command, out)
			}
		}
	}
}

// A read-only command in the linked worktree stays allowed.
func TestHookBash_LinkedWorktreeReadIsAllowed(t *testing.T) {
	fenceTempRootsElsewhere(t)
	repo := tempRepo(t)
	t.Chdir(repo)
	t.Setenv("SATELLE_PROJECT_DIR", repo)
	gitInitRepo(t, repo)
	wt := linkedWorktree(t, repo)

	for _, hook := range []string{"gate", "commitgate"} {
		if out, err := runRootIn(t, bashEvent("cat "+filepath.Join(wt, "f.txt")), "hook", hook); err != nil {
			t.Errorf("%s: a read in the linked worktree must be allowed: %v\n%s", hook, err, out)
		}
	}
}

// AC1: the fence stepping aside for a linked worktree must not open its
// substrate. The lock list is repo-relative, so it is resolved against the tree
// the target lives in: under a performing seat, an edit of the sibling tree's
// .satelle/ is refused by the substrate lock (a story may not rewrite its own
// judge), not waved through and not refused as a foreign tree.
func TestSubstrateLock_LinkedWorktreeSubstrateStaysLocked(t *testing.T) {
	fenceTempRootsElsewhere(t)
	repo, id := lockRepo(t, "feature", "")
	t.Setenv("SATELLE_PROJECT_DIR", repo)
	gitInitRepo(t, repo)
	wt := linkedWorktree(t, repo)

	for _, rel := range []string{".satelle/skills/x.md", ".satelle/workflows/agents.toml", ".satelle/satelle.toml"} {
		out, err := runRootIn(t, claudeEditEvent(filepath.Join(wt, rel)), "hook", "gate")
		if err == nil {
			t.Fatalf("%s in the linked worktree must be substrate-locked while %s holds a seat:\n%s", rel, id, out)
		}
		reason := denyReasonOf(t, out)
		if strings.Contains(reason, "another repo's tree") {
			t.Errorf("%s: refused as a foreign tree, not locked: %s", rel, reason)
		}
		for _, want := range []string{id, rel, "substrate lock"} {
			if !strings.Contains(reason, want) {
				t.Errorf("deny for %s missing %q: %s", rel, want, reason)
			}
		}
	}
}

// AC3: an anchor that itself lives under a temp root still fences a foreign repo
// outside it — the temp exemption never covers the session's own tree.
func TestForeignTreeTarget_TempExemptionNeverCoversOwnTree(t *testing.T) {
	base := t.TempDir()
	old := containmentTempRoots
	containmentTempRoots = func() []string { return []string{base} }
	t.Cleanup(func() { containmentTempRoots = old })

	anchor := gitInitRepo(t, filepath.Join(base, "home"))
	other := gitInitRepo(t, t.TempDir())
	if _, _, ok := foreignTreeTarget(anchor, []string{filepath.Join(other, "f.txt")}); !ok {
		t.Error("an anchor under the temp root must still fence a foreign repo")
	}
	if _, _, ok := foreignTreeTarget(anchor, []string{filepath.Join(anchor, "f.txt")}); ok {
		t.Error("the session's own tree is never foreign")
	}
}
