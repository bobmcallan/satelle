package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_92e4cdbe: a story engaged from a linked worktree makes its substrate edits
// in the MAIN tree (the process of record), outside its own git slice.

type wtWiring int

const (
	wireNone     wtWiring = iota // neither probe nor worktree config
	wireConfig                   // SetWorktreeConfig only
	wireProbeCfg                 // SetWorktreeConfig + SetProcessProbe, as internal/cli does
)

// substrateFixture builds a main repo whose substrate dirs are wired as the CLI
// wires them, engages a story from cwdTree (the main tree, or a linked worktree)
// and returns the story plus the main tree's skills dir.
type substrateFixture struct {
	main, wt, skills string
	story            workitem.Item
}

func newSubstrateFixture(t *testing.T, fromWorktree bool, wiring wtWiring) substrateFixture {
	t.Helper()
	withWiring(t)
	main := gitRepo(t)
	skills := filepath.Join(main, ".satelle", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	f := substrateFixture{main: main, skills: skills}
	tree := main
	if fromWorktree {
		f.wt = filepath.Join(t.TempDir(), "wt")
		if out, err := exec.Command("git", "-C", main, "worktree", "add", "-b", "wt-branch", f.wt).CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v\n%s", err, out)
		}
		tree = f.wt
	}
	chdir(t, tree)

	stories := filepath.Join(t.TempDir(), "stories")
	if err := os.MkdirAll(stories, 0o755); err != nil {
		t.Fatal(err)
	}
	wireWithWorkflows(t, changeWF)
	verb.SetStoryDir(stories)
	verb.SetAuthoredDirs(map[string]string{"skills": skills})
	verb.SetSubstrateConfigDir(filepath.Join(main, ".satelle"))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	if wiring != wireNone {
		verb.SetWorktreeConfig(config.Config{}, main)
	}
	if wiring == wireProbeCfg {
		verb.SetProcessProbe(&verb.ProcessProbe{ProcessRoot: main, InvokingRoot: tree})
	}

	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "wt substrate", "body": "g", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &f.story)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": f.story.ID, "status": "in_progress"}), &f.story)
	return f
}

// touchSkill writes a main-tree skill after the engagement anchor.
func (f substrateFixture) touchSkill(t *testing.T) {
	t.Helper()
	p := filepath.Join(f.skills, "new.md")
	if err := os.WriteFile(p, []byte("# s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
}

type diffOut struct {
	Files            []string `json:"files"`
	ProcessRoot      string   `json:"process_root"`
	ProcessRootFiles []string `json:"process_root_files"`
	Note             string   `json:"note"`
}

func (f substrateFixture) diff(t *testing.T, extra map[string]any) (diffOut, map[string]any) {
	t.Helper()
	req := map[string]any{"id": f.story.ID}
	for k, v := range extra {
		req[k] = v
	}
	raw := call(t, "story-diff", req)
	var d diffOut
	var top map[string]any
	json.Unmarshal(raw, &d)
	json.Unmarshal(raw, &top)
	return d, top
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

const mainSkill = ".satelle/skills/new.md"

// AC1: the process-root substrate is enumerated, named, and located.
func TestStoryDiffWorktreeEnumeratesProcessRootSubstrate(t *testing.T) {
	for name, wiring := range map[string]wtWiring{"probe": wireProbeCfg, "worktree-config-only": wireConfig} {
		t.Run(name, func(t *testing.T) {
			f := newSubstrateFixture(t, true, wiring)
			f.touchSkill(t)
			d, _ := f.diff(t, map[string]any{"include_substrate": true})
			if !has(d.Files, mainSkill) {
				t.Errorf("files should name the main-tree skill: %v", d.Files)
			}
			if !has(d.ProcessRootFiles, mainSkill) {
				t.Errorf("process_root_files should name it: %v", d.ProcessRootFiles)
			}
			if d.ProcessRoot != f.main {
				t.Errorf("process_root=%q want %q", d.ProcessRoot, f.main)
			}
			if !strings.Contains(d.Note, f.main) {
				t.Errorf("note should say the files live in the main tree: %q", d.Note)
			}
		})
	}
}

// AC1: with no process-of-record wiring a root is never guessed.
func TestStoryDiffWorktreeUnwiredDropsProcessRootSubstrate(t *testing.T) {
	f := newSubstrateFixture(t, true, wireNone)
	f.touchSkill(t)
	d, top := f.diff(t, map[string]any{"include_substrate": true})
	if has(d.Files, mainSkill) {
		t.Errorf("unwired: main-tree path must stay dropped: %v", d.Files)
	}
	for _, k := range []string{"process_root", "process_root_files"} {
		if _, ok := top[k]; ok {
			t.Errorf("unwired: %s must be omitted", k)
		}
	}
}

// AC2: the transition-time change record carries the same paths.
func TestChangeRecordWorktreeIncludesProcessRootSubstrate(t *testing.T) {
	f := newSubstrateFixture(t, true, wireProbeCfg)
	f.touchSkill(t)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": f.story.ID, "status": "done"}), &f.story)
	d, _ := f.diff(t, map[string]any{"recorded": true})
	if !has(d.Files, mainSkill) {
		t.Errorf("recorded change set should name the main-tree skill: %v", d.Files)
	}
}

// AC3: what the satelle-substrate-only-check script reads — a non-empty change
// set whose every path matches its `.satelle/` allow prefix.
func TestWorktreeSubstrateChangeSetSatisfiesSubstrateOnlyCheck(t *testing.T) {
	f := newSubstrateFixture(t, true, wireProbeCfg)
	f.touchSkill(t)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": f.story.ID, "status": "done"}), &f.story)
	live, _ := f.diff(t, map[string]any{"include_substrate": true})
	rec, _ := f.diff(t, map[string]any{"recorded": true})
	allow := regexp.MustCompile(`^(\.satelle/|docs/|\.gitignore$|\.claude/|\.grok/)`)
	for name, files := range map[string][]string{"live": live.Files, "recorded": rec.Files} {
		if len(files) == 0 {
			t.Errorf("%s: change set empty — the check would reject with 'no change set found'", name)
		}
		for _, p := range files {
			if !allow.MatchString(p) {
				t.Errorf("%s: %q is outside the allowed substrate prefixes", name, p)
			}
		}
	}
}

// AC4: a story engaged in the main tree is unchanged and reports no process root.
func TestStoryDiffMainTreeOmitsProcessRootFields(t *testing.T) {
	f := newSubstrateFixture(t, false, wireProbeCfg)
	f.touchSkill(t)
	d, top := f.diff(t, map[string]any{"include_substrate": true})
	if !has(d.Files, mainSkill) {
		t.Errorf("main-tree substrate must still be listed: %v", d.Files)
	}
	for _, k := range []string{"process_root", "process_root_files"} {
		if _, ok := top[k]; ok {
			t.Errorf("main-tree story: %s must be omitted", k)
		}
	}
}
