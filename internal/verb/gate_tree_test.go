package verb_test

import (
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
