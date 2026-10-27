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
func engageInTrunkClone(t *testing.T) *trunkStory { return engageTrunkClone(t, "") }

// engageTrunkClone is engageInTrunkClone for a workflow that also declares park
// (a line of the done half, e.g. `park = { state = "blocked" }`).
func engageTrunkClone(t *testing.T, park string) *trunkStory {
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
`+park+`
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
	Files    []string `json:"files"`
	Stat     string   `json:"stat"`
	Patch    string   `json:"patch"`
	Baseline string   `json:"baseline_sha"`
	Note     string   `json:"note"`
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

const parkBlocked = `park = { state = "blocked" }`

// set moves the story to status.
func (ts *trunkStory) set(status string) {
	ts.t.Helper()
	json.Unmarshal(call(ts.t, "story-set", map[string]any{"id": ts.it.ID, "status": status}), &ts.it)
}

// park and resume leave and re-enter the engaged state through the blocked park.
func (ts *trunkStory) park()   { ts.t.Helper(); ts.set("blocked") }
func (ts *trunkStory) resume() { ts.t.Helper(); ts.set("in_progress") }

// mergeTrunk merges the remote trunk into the story's branch the way a branch
// brings main in for an up-to-date requirement: a merge commit only the story holds.
func (ts *trunkStory) mergeTrunk() {
	ts.t.Helper()
	ts.r.Git(ts.t, ts.r.Subject, "fetch", "--quiet", "origin")
	ts.r.Git(ts.t, ts.r.Subject, "merge", "--quiet", "--no-ff", "-m", "Merge main into the branch", "origin/main")
}

// A story that commits, parks and resumes keeps its pre-park commits in story diff
// and story proof alongside the ones made after the resume (sty_666e6110 AC1).
func TestResumedStoryKeepsPreParkCommits(t *testing.T) {
	ts := engageTrunkClone(t, parkBlocked)
	ts.commit(ts.r.Subject, "a.go", "add a.go")
	if err := os.WriteFile(filepath.Join(ts.r.Subject, "a_test.go"), []byte("package p\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts.r.Git(t, ts.r.Subject, "add", "a_test.go")
	ts.r.Git(t, ts.r.Subject, "commit", "--quiet", "-m", "add a_test.go")
	ts.park()
	ts.resume()
	ts.commit(ts.r.Subject, "b.go", "add b.go")

	wantFiles(t, "live", ts.live().Files, []string{"a.go", "a_test.go", "b.go"}, nil)

	raw, err := dispatchRaw(t, "story-proof", map[string]any{"id": ts.it.ID})
	if err != nil {
		t.Fatal(err)
	}
	proof := decodeProof(t, raw)
	if len(proof.Tests) != 1 || proof.Tests[0].Path != "a_test.go" {
		t.Errorf("story proof tests=%#v want the pre-park a_test.go", proof.Tests)
	}
}

// Commits another story lands while this one is parked stay out of its change set
// whether the remote trunk holds them or not, unless their subject names the story
// (sty_666e6110 AC2).
func TestResumedStoryExcludesParkTimeForeignCommits(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "local only"
		if published {
			name = "pushed"
		}
		t.Run(name, func(t *testing.T) {
			ts := engageTrunkClone(t, parkBlocked)
			ts.own("a.go")
			ts.park()
			ts.commit(ts.r.Subject, "foreign.go", "feat: foreign (sty_other)")
			if published {
				ts.r.Git(t, ts.r.Subject, "push", "--quiet", "origin", "main")
			}
			ts.own("named.go")
			ts.resume()
			ts.commit(ts.r.Subject, "b.go", "add b.go")

			v := ts.live()
			wantFiles(t, "live", v.Files, []string{"a.go", "named.go", "b.go"}, []string{"foreign.go"})
			if strings.Contains(v.Patch, "foreign.go") || strings.Contains(v.Stat, "foreign.go") {
				t.Errorf("patch/stat must not mention the foreign file:\n%s\n%s", v.Stat, v.Patch)
			}
		})
	}
}

// Merging the trunk into a resumed story's branch adds only the merge's own
// conflict resolutions, never the files the trunk brought in (sty_666e6110 AC3).
func TestResumedStoryTrunkMergeContributesOnlyResolutions(t *testing.T) {
	t.Run("clean merge", func(t *testing.T) {
		ts := engageTrunkClone(t, parkBlocked)
		ts.commit(ts.r.Subject, "a.go", "add a.go")
		ts.park()
		ts.resume()
		ts.commit(ts.r.Subject, "b.go", "add b.go")
		ts.land("trunk_only.go", "feat: other (sty_other)")
		ts.mergeTrunk()
		wantFiles(t, "live", ts.live().Files, []string{"a.go", "b.go"}, []string{"trunk_only.go"})
	})
	t.Run("conflict resolution", func(t *testing.T) {
		ts := engageTrunkClone(t, parkBlocked)
		ts.commit(ts.r.Subject, "a.go", "add a.go")
		ts.park()
		ts.resume()
		ts.r.Commit(t, ts.r.Subject, "shared.go", "mine\n")
		ts.r.Git(t, ts.r.Pusher, "pull", "--quiet", "--ff-only")
		ts.r.Commit(t, ts.r.Pusher, "shared.go", "theirs\n")
		ts.r.Git(t, ts.r.Pusher, "push", "--quiet", "origin", "main")
		ts.land("trunk_only.go", "feat: other too (sty_other)")
		ts.r.Git(t, ts.r.Subject, "fetch", "--quiet", "origin")
		if out, err := ts.r.GitErr(ts.r.Subject, "merge", "--no-ff", "-m", "Merge main into the branch", "origin/main"); err == nil {
			t.Fatalf("setup: want a conflict on shared.go, merged cleanly:\n%s", out)
		}
		if err := os.WriteFile(filepath.Join(ts.r.Subject, "shared.go"), []byte("resolved\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts.r.Git(t, ts.r.Subject, "add", "shared.go")
		ts.r.Git(t, ts.r.Subject, "commit", "--quiet", "--no-edit")

		v := ts.live()
		wantFiles(t, "live", v.Files, []string{"a.go", "shared.go"}, []string{"trunk_only.go"})
		if !strings.Contains(v.Patch, "+resolved") || strings.Contains(v.Patch, "trunk_only.go") {
			t.Errorf("patch must carry the resolution and not the trunk's file:\n%s", v.Patch)
		}
	})
}

// The id4me shape — commit, park (another story's commit lands), resume, trunk
// merge — hands the reviewer exactly the story's own files, measured from the
// engagement baseline, with the park interval named in the note (sty_666e6110 AC4).
func TestId4meResumeMergeShape(t *testing.T) {
	ts := engageTrunkClone(t, parkBlocked)
	engaged := ts.r.Head(t, ts.r.Subject)
	var own []string
	for i := 1; i <= 9; i++ {
		f := "check_contacts_" + string(rune('0'+i)) + ".go"
		own = append(own, f)
		ts.commit(ts.r.Subject, f, "add "+f)
	}
	parked := ts.r.Head(t, ts.r.Subject)
	ts.park()
	ts.commit(ts.r.Subject, "infisical.go", "feat: infisical (sty_other)")
	resumed := ts.r.Head(t, ts.r.Subject)
	ts.resume()
	ts.land("infisical_remote.go", "feat: infisical remote (sty_other)")
	ts.mergeTrunk()

	v := ts.live()
	wantFiles(t, "live", v.Files, own, []string{"infisical.go", "infisical_remote.go"})
	if len(v.Files) != len(own) {
		t.Errorf("files=%v want exactly the story's %d own files", v.Files, len(own))
	}
	if v.Baseline != engaged {
		t.Errorf("baseline_sha=%q want engagement baseline %q", v.Baseline, engaged)
	}
	if want := "excluded park interval " + parked + ".." + resumed; !strings.Contains(v.Note, want) {
		t.Errorf("note=%q want it to name %q", v.Note, want)
	}
}

// A story that never parked and merges the trunk into its branch drops the files
// the trunk brought in and keeps its own (sty_666e6110 AC5).
func TestNeverParkedTrunkMergeKeepsOnlyOwn(t *testing.T) {
	ts := engageInTrunkClone(t)
	ts.commit(ts.r.Subject, "o.go", "add o.go")
	ts.land("trunk_only.go", "feat: other (sty_other)")
	ts.mergeTrunk()
	wantFiles(t, "live", ts.live().Files, []string{"o.go"}, []string{"trunk_only.go"})
}
