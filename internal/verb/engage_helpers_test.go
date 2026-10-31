package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// twoWorktrees builds a real git repo with a real second working tree. Returns
// both toplevels; the caller chdirs between them. Both trees share one git
// common dir, so they share one satelle DB (config.RepoKey).
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

func moveTo(t *testing.T, id, status string) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": id, "status": status}), &it)
	if it.Status != status {
		t.Fatalf("%s → %s: got %q", id, status, it.Status)
	}
	return it
}

// isPerforming reports whether id's chain folds to a performing state — the
// count [engagement] limit and a sequential epic read.
func isPerforming(t *testing.T, id string) bool {
	t.Helper()
	performing, err := verb.PerformingStories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range performing {
		if it.ID == id {
			return true
		}
	}
	return false
}

// presenceRow is id's newest working_on row written at this location.
func presenceRow(t *testing.T, id string) (verb.WorkingOnRow, bool) {
	t.Helper()
	return verb.LatestLocalWorkingOn(context.Background(), id)
}
