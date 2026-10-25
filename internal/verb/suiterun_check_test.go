package verb_test

// Tree-keyed check evidence (sty_dc554488): the suite_run row a check_reuse:
// tree gate records, the lookup that finds it, and the tree hash it is keyed on.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

func evidence(tree, outcome string) verb.SuiteEvidence {
	return verb.SuiteEvidence{
		Check: "satelle-x-check", Tree: tree, SHA: "abc", Command: "make x",
		CommandSHA: verb.CommandDigest("make x"), Outcome: outcome, DurationMS: 1500,
	}
}

// A recorded green run reads back with every field; a red run, another tree,
// another command, another check and another story are not found.
func TestCheckRunRoundTrip(t *testing.T) {
	wire(t)
	suiteRepo(t)
	it := newStory(t, "records a check run")
	ctx := context.Background()

	id, err := verb.RecordCheckRun(ctx, it.ID, evidence("tree1", verb.SuiteGreen))
	if err != nil || id == "" {
		t.Fatalf("record: id=%q err=%v", id, err)
	}
	got, ok := verb.FindCheckRun(ctx, it.ID, "satelle-x-check", verb.CommandDigest("make x"), "tree1")
	if !ok {
		t.Fatal("green run not found")
	}
	if got.ID != id || got.Check != "satelle-x-check" || got.Tree != "tree1" || got.SHA != "abc" ||
		got.Command != "make x" || got.Outcome != verb.SuiteGreen || got.DurationMS != 1500 || got.RecordedAt.IsZero() {
		t.Errorf("fields = %+v", got)
	}
	for name, c := range map[string]struct{ story, check, cmd, tree string }{
		"other tree":    {it.ID, "satelle-x-check", verb.CommandDigest("make x"), "tree2"},
		"other command": {it.ID, "satelle-x-check", verb.CommandDigest("make y"), "tree1"},
		"other check":   {it.ID, "satelle-y-check", verb.CommandDigest("make x"), "tree1"},
		"other story":   {"sty_other", "satelle-x-check", verb.CommandDigest("make x"), "tree1"},
	} {
		if _, ok := verb.FindCheckRun(ctx, c.story, c.check, c.cmd, c.tree); ok {
			t.Errorf("%s: found a run it must not match", name)
		}
	}
}

func TestCheckRunRedIsNeverFound(t *testing.T) {
	wire(t)
	suiteRepo(t)
	it := newStory(t, "records a red check run")
	ctx := context.Background()
	if _, err := verb.RecordCheckRun(ctx, it.ID, evidence("tree1", verb.SuiteRed)); err != nil {
		t.Fatal(err)
	}
	if _, ok := verb.FindCheckRun(ctx, it.ID, "satelle-x-check", verb.CommandDigest("make x"), "tree1"); ok {
		t.Error("a red run was returned for reuse")
	}
}

// The tree hash is stable on an unchanged tree and moves with a tracked edit, an
// untracked file and a new commit's content; an ignored file does not move it,
// and the repository's own index is never touched.
func TestGitContentTree(t *testing.T) {
	dir, commit := suiteRepo(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree := func() string {
		t.Helper()
		tr, head, err := verb.GitContentTree(dir)
		if err != nil || tr == "" || head == "" {
			t.Fatalf("GitContentTree: %q %q %v", tr, head, err)
		}
		return tr
	}
	write(".gitignore", "ignored.txt\n")
	base := tree()
	if again := tree(); again != base {
		t.Errorf("unchanged tree hashed twice: %s vs %s", base, again)
	}
	write("ignored.txt", "x")
	if got := tree(); got != base {
		t.Error("an ignored file moved the hash")
	}
	write("seed.txt", "edited\n")
	edited := tree()
	if edited == base {
		t.Error("a tracked edit did not move the hash")
	}
	write("new.txt", "n")
	untracked := tree()
	if untracked == edited {
		t.Error("an untracked file did not move the hash")
	}
	if out, _ := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("the repository index was touched: %s", out)
	}
	// Committing what is already in the working tree changes no content.
	exec.Command("git", "-C", dir, "add", "-A").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "slice").Run()
	if got := tree(); got != untracked {
		t.Error("committing the same content moved the hash")
	}
	commit("another.txt")
	if got := tree(); got == untracked {
		t.Error("a new file did not move the hash")
	}
}

func TestGitContentTreeNotARepo(t *testing.T) {
	if _, _, err := verb.GitContentTree(t.TempDir()); err == nil {
		t.Error("a directory with no git repo must error so the check runs unrecorded")
	}
}
