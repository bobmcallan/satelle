package verb_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// trunkStory is a story engaged in the subject clone of a bare remote, the shape
// of a main tree that fast-forwards while other stories merge (sty_659c4133).
type trunkStory struct {
	t  *testing.T
	r  testutil.TrunkRepos
	it workitem.Item
}

// engageInTrunkClone engages a story in the subject clone of a fresh remote.
func engageInTrunkClone(t *testing.T) *trunkStory {
	t.Helper()
	withWiring(t)
	r := testutil.NewTrunkRepos(t)
	cwd, _ := os.Getwd()
	if err := os.Chdir(r.Subject); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	wireWithWorkflows(t, routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`))
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	ts := &trunkStory{t: t, r: r}
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "trunk story", "body": "body", "acceptance_criteria": "1. x",
		"category": "feature", "tags": []string{"workflow:eng"},
	}), &ts.it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": ts.it.ID, "status": "in_progress"}), &ts.it)
	return ts
}

// land publishes one commit from the pusher, the way another story's merge does:
// the file under the given subject, pushed to the remote trunk.
func (ts *trunkStory) land(file, subject string) {
	ts.t.Helper()
	ts.r.Git(ts.t, ts.r.Pusher, "pull", "--quiet", "--ff-only")
	ts.commit(ts.r.Pusher, file, subject)
	ts.r.Git(ts.t, ts.r.Pusher, "push", "--quiet", "origin", "main")
}

// commit commits file in dir under subject.
func (ts *trunkStory) commit(dir, file, subject string) {
	ts.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		ts.t.Fatal(err)
	}
	ts.r.Git(ts.t, dir, "add", file)
	ts.r.Git(ts.t, dir, "commit", "--quiet", "-m", subject)
}

// own commits file in the subject clone under a subject that names the story.
func (ts *trunkStory) own(file string) { ts.commit(ts.r.Subject, file, "fix: "+file+" ("+ts.it.ID+")") }

// fastForward brings the remote trunk into the subject clone, as a main-tree pull does.
func (ts *trunkStory) fastForward() {
	ts.t.Helper()
	ts.r.Git(ts.t, ts.r.Subject, "pull", "--quiet", "--ff-only")
}

type diffView struct {
	Files []string `json:"files"`
	Stat  string   `json:"stat"`
	Patch string   `json:"patch"`
}

func (ts *trunkStory) live() diffView {
	ts.t.Helper()
	var v diffView
	json.Unmarshal(call(ts.t, "story-diff", map[string]any{"id": ts.it.ID, "patch": true}), &v)
	return v
}

func (ts *trunkStory) recorded() []string {
	ts.t.Helper()
	var v diffView
	json.Unmarshal(call(ts.t, "story-diff", map[string]any{"id": ts.it.ID, "recorded": true}), &v)
	return v.Files
}

func wantFiles(t *testing.T, what string, got []string, in, notIn []string) {
	t.Helper()
	for _, f := range in {
		if !containsPath(got, f) {
			t.Errorf("%s: %s missing from %v", what, f, got)
		}
	}
	for _, f := range notIn {
		if containsPath(got, f) {
			t.Errorf("%s: %s must not be in %v", what, f, got)
		}
	}
}

// Another story's commit that a fast-forward put under this story is not this
// story's change, on both channels: live story-diff (files, stat and patch) and the
// recorded change_record union. The story's own commit, its uncommitted edit to a
// tracked file and its untracked file stay (sty_659c4133 AC1).
func TestStoryDiffExcludesForeignTrunkCommitsAfterFastForward(t *testing.T) {
	ts := engageInTrunkClone(t)
	ts.land("Makefile", "feat: x (sty_other)")
	ts.fastForward()
	ts.own("a.go")
	if err := os.WriteFile(filepath.Join(ts.r.Subject, "seed.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ts.r.Subject, "untracked.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := ts.live()
	wantFiles(t, "live", v.Files, []string{"a.go", "seed.txt", "untracked.txt"}, []string{"Makefile"})
	if strings.Contains(v.Patch, "Makefile") || strings.Contains(v.Stat, "Makefile") {
		t.Errorf("live patch/stat must not mention the foreign file:\n%s\n%s", v.Stat, v.Patch)
	}
	if !strings.Contains(v.Patch, "a.go") || !strings.Contains(v.Patch, "seed.txt") {
		t.Errorf("live patch must carry the story's own hunks:\n%s", v.Patch)
	}

	json.Unmarshal(call(t, "story-set", map[string]any{"id": ts.it.ID, "status": "done"}), &ts.it)
	wantFiles(t, "recorded", ts.recorded(), []string{"a.go"}, []string{"Makefile"})
}

// Controls: with no remote trunk to compare with the foreign commit is listed as
// before, and a commit that touches a path under the story's own subject keeps it.
func TestStoryDiffForeignTrunkCommitControls(t *testing.T) {
	t.Run("no remote", func(t *testing.T) {
		ts := engageInTrunkClone(t)
		ts.land("Makefile", "feat: x (sty_other)")
		ts.fastForward()
		ts.r.Git(t, ts.r.Subject, "remote", "remove", "origin")
		wantFiles(t, "live", ts.live().Files, []string{"Makefile"}, nil)
	})
	t.Run("own commit on the same path", func(t *testing.T) {
		ts := engageInTrunkClone(t)
		ts.land("shared.go", "feat: shared (sty_other)")
		ts.fastForward()
		ts.r.Git(t, ts.r.Subject, "commit", "--quiet", "--allow-empty", "-m", "noop")
		if err := os.WriteFile(filepath.Join(ts.r.Subject, "shared.go"), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts.r.Git(t, ts.r.Subject, "commit", "--quiet", "-am", "fix: shared ("+ts.it.ID+")")
		v := ts.live()
		wantFiles(t, "live", v.Files, []string{"shared.go"}, nil)
		if !strings.Contains(v.Patch, "+mine") || strings.Contains(v.Patch, "+shared.go") {
			t.Errorf("patch must carry only the own commit's hunk:\n%s", v.Patch)
		}
	})
}

// A commit that names the story stays in its change set although the remote trunk
// holds it, wherever it sits among other stories' published commits (AC3).
func TestStoryDiffKeepsOwnCommitOnTrunk(t *testing.T) {
	t.Run("own then foreign", func(t *testing.T) {
		ts := engageInTrunkClone(t)
		ts.own("own.go")
		ts.r.Git(t, ts.r.Subject, "push", "--quiet", "origin", "main")
		ts.land("other.go", "feat: other (sty_other)")
		ts.fastForward()
		wantFiles(t, "live", ts.live().Files, []string{"own.go"}, []string{"other.go"})
	})
	t.Run("foreign then own", func(t *testing.T) {
		ts := engageInTrunkClone(t)
		ts.land("other.go", "feat: other (sty_other)")
		ts.fastForward()
		ts.own("own.go")
		ts.r.Git(t, ts.r.Subject, "push", "--quiet", "origin", "main")
		wantFiles(t, "live", ts.live().Files, []string{"own.go"}, []string{"other.go"})
	})
	t.Run("merge naming the story", func(t *testing.T) {
		ts := engageInTrunkClone(t)
		ts.r.Git(t, ts.r.Pusher, "checkout", "--quiet", "-b", "side")
		ts.commit(ts.r.Pusher, "merged.go", "work on a side branch")
		ts.r.Git(t, ts.r.Pusher, "checkout", "--quiet", "main")
		ts.r.Git(t, ts.r.Pusher, "merge", "--quiet", "--no-ff", "-m", "Merge PR for "+ts.it.ID, "side")
		ts.r.Git(t, ts.r.Pusher, "push", "--quiet", "origin", "main")
		ts.land("other.go", "feat: other (sty_other)")
		ts.fastForward()
		wantFiles(t, "live", ts.live().Files, []string{"merged.go"}, []string{"other.go"})
	})
}
