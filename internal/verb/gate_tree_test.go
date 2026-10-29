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
)

// sty_ab93f9a6: a gate's functional check runs in the MAIN tree; `story diff`
// resolves the story's own tree only for the story the gate names, and only
// inside the same repository. Every other foreign-tree diff is still refused.

const foreignTreeRefusal = "was engaged from working tree"

// diffErr runs story-diff for id from the current directory.
func diffErr(t *testing.T, id string) (string, error) {
	t.Helper()
	out, err := dispatchRaw(t, "story-diff", map[string]any{"id": id, "include_substrate": true})
	return string(out), err
}

func TestStoryDiffGateTreeOverride(t *testing.T) {
	t.Run("no gate context refuses", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		chdir(t, f.main)
		if _, err := diffErr(t, f.story.ID); err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Fatalf("a diff from the main tree must be refused without gate context: %v", err)
		}
	})

	t.Run("gate context for a different story refuses", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		chdir(t, f.main)
		t.Setenv(verb.GateStoryEnv, "sty_someoneelse")
		if _, err := diffErr(t, f.story.ID); err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Fatalf("gate context naming another story must not lift the refusal: %v", err)
		}
	})

	t.Run("anchor outside the invoking repository refuses", func(t *testing.T) {
		f := newSubstrateFixture(t, false, wireProbeCfg)
		chdir(t, gitRepo(t)) // an unrelated repository
		t.Setenv(verb.GateStoryEnv, f.story.ID)
		if _, err := diffErr(t, f.story.ID); err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Fatalf("gate context must not reach a tree of another repository: %v", err)
		}
	})

	t.Run("gate context for this story reads its worktree", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		f.touchSkill(t)
		chdir(t, f.main)
		t.Setenv(verb.GateStoryEnv, f.story.ID)
		d, _ := f.diff(t, map[string]any{"include_substrate": true})
		if !has(d.Files, mainSkill) {
			t.Errorf("the story's change set must include the main-tree skill: %v", d.Files)
		}
	})

	t.Run("main-tree story is unaffected by gate context", func(t *testing.T) {
		f := newSubstrateFixture(t, false, wireProbeCfg)
		f.touchSkill(t)
		t.Setenv(verb.GateStoryEnv, f.story.ID)
		d, _ := f.diff(t, map[string]any{"include_substrate": true})
		if !has(d.Files, mainSkill) {
			t.Errorf("main-tree story must list its substrate: %v", d.Files)
		}
	})
}

// The in-process gate mark (WithGateStory) lifts the refusal exactly as
// GateStoryEnv does, with no environment variable set: for the story it names,
// inside the same repository, and nowhere else.
func TestStoryDiffWithGateStoryMark(t *testing.T) {
	commitInWorktree := func(t *testing.T, f substrateFixture) {
		t.Helper()
		gitIn := func(args ...string) {
			if out, err := exec.Command("git", append([]string{"-C", f.wt}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(f.wt, "slice.go"), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn("add", "slice.go")
		gitIn("commit", "-qm", "slice")
	}

	t.Run("mark for this story reads its worktree", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		commitInWorktree(t, f)
		chdir(t, f.main)
		t.Setenv(verb.GateStoryEnv, "")
		d, err := verb.StoryDiff(verb.WithGateStory(context.Background(), f.story.ID), f.story.ID, verb.StoryDiffOpts{})
		if err != nil || !has(d.Files, "slice.go") {
			t.Fatalf("the marked diff must read the story's own worktree: files %v, err %v", d.Files, err)
		}
		if _, err := diffErr(t, f.story.ID); err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Errorf("an unmarked diff from the same tree must still be refused: %v", err)
		}
	})

	t.Run("mark for a different story refuses", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		chdir(t, f.main)
		_, err := verb.StoryDiff(verb.WithGateStory(context.Background(), "sty_someoneelse"), f.story.ID, verb.StoryDiffOpts{})
		if err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Fatalf("a mark naming another story must not lift the refusal: %v", err)
		}
	})

	t.Run("mark cannot reach another repository", func(t *testing.T) {
		f := newSubstrateFixture(t, false, wireProbeCfg)
		chdir(t, gitRepo(t))
		_, err := verb.StoryDiff(verb.WithGateStory(context.Background(), f.story.ID), f.story.ID, verb.StoryDiffOpts{})
		if err == nil || !strings.Contains(err.Error(), foreignTreeRefusal) {
			t.Fatalf("the mark must not reach a tree of another repository: %v", err)
		}
	})
}

// StoryGateTree names the tree a plan review's citations are read from. The
// readiness edge runs before any engagement baseline exists, so it must resolve
// without one.
func TestStoryGateTreeWithoutBaseline(t *testing.T) {
	canon := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}

	t.Run("a story never engaged resolves to the invoking toplevel", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		var fresh struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(call(t, "story-create", map[string]any{
			"title": "never engaged", "body": "g", "acceptance_criteria": "1. ok",
			"category": "feature", "tags": []string{"workflow:cr-wf"},
		}), &fresh); err != nil || fresh.ID == "" {
			t.Fatalf("story-create: %v %+v", err, fresh)
		}
		chdir(t, f.main)
		got, err := verb.StoryGateTree(context.Background(), fresh.ID)
		if err != nil {
			t.Fatalf("a story with no engagement baseline must still resolve: %v", err)
		}
		if canon(got) != canon(f.main) {
			t.Errorf("tree = %q, want the invoking toplevel %q", got, f.main)
		}
	})

	t.Run("an engaged story under its gate context resolves to its own worktree", func(t *testing.T) {
		f := newSubstrateFixture(t, true, wireProbeCfg)
		chdir(t, f.main)
		if got, err := verb.StoryGateTree(context.Background(), f.story.ID); err != nil || canon(got) != canon(f.main) {
			t.Errorf("without gate context the invoking tree stands: %q, %v", got, err)
		}
		t.Setenv(verb.GateStoryEnv, f.story.ID)
		got, err := verb.StoryGateTree(context.Background(), f.story.ID)
		if err != nil || canon(got) != canon(f.wt) {
			t.Errorf("tree = %q, %v; want the story's worktree %q", got, err, f.wt)
		}
	})
}
