package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A story that changes only gitignored substrate leaves git nothing to diff, so
// the reviewer's diff material must carry the substrate itself (sty_98a2816b).

type substrateEnv struct {
	t        *testing.T
	dir      string
	skillDir string
	db       *store.DB
}

// newSubstrateEnv is a git repo with .satelle/ ignored (the posture of a repo
// that keeps its substrate out of git), the verb layer wired over a real store,
// and the skills dir registered as authored substrate.
func newSubstrateEnv(t *testing.T) *substrateEnv {
	t.Helper()
	withWiring(t)
	dir := gitRepo(t)
	chdir(t, dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".satelle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".gitignore")
	run("git", "commit", "-m", "ignore satelle")
	skillDir := filepath.Join(dir, ".satelle", "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := wireWithWorkflowsStore(t, changeWF)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))
	verb.SetAuthoredDirs(map[string]string{"skills": skillDir})
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	return &substrateEnv{t: t, dir: dir, skillDir: skillDir, db: db}
}

func (e *substrateEnv) story(title string) workitem.Item {
	e.t.Helper()
	var it workitem.Item
	json.Unmarshal(call(e.t, "story-create", map[string]any{
		"title": title, "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	}), &it)
	return it
}

func (e *substrateEnv) set(id, status string) workitem.Item {
	e.t.Helper()
	var it workitem.Item
	json.Unmarshal(call(e.t, "story-set", map[string]any{"id": id, "status": status}), &it)
	if it.Status != status {
		e.t.Fatalf("%s status = %q, want %q", id, it.Status, status)
	}
	return it
}

// write puts a skill file on disk, mtime pushed past the engagement baseline so
// a coarse filesystem clock cannot place it before the ledger's timestamp.
func (e *substrateEnv) write(name, body string) string {
	e.t.Helper()
	path := filepath.Join(e.skillDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	at := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		e.t.Fatal(err)
	}
	return ".satelle/skills/" + name
}

func (e *substrateEnv) diff(id string, opts verb.StoryDiffOpts) verb.StoryDiffResult {
	e.t.Helper()
	res, err := verb.StoryDiff(context.Background(), id, opts)
	if err != nil {
		e.t.Fatalf("StoryDiff(%s): %v", id, err)
	}
	return res
}

func (e *substrateEnv) baselineAt(id string) time.Time {
	e.t.Helper()
	rows, err := e.db.Ledger.ListByStory(context.Background(), id, ledger.KindEngagementBaseline)
	if err != nil || len(rows) == 0 {
		e.t.Fatalf("no engagement baseline for %s: %v", id, err)
	}
	return rows[0].CreatedAt
}

func (e *substrateEnv) append(storyID, kind, to string, at time.Time) {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]string{"from": "in_progress", "to": to})
	if _, err := e.db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: storyID, Kind: kind, Actor: "executor", Payload: payload,
	}, at); err != nil {
		e.t.Fatal(err)
	}
}

// AC1, AC2, AC5: the opted-in live diff lists a new gitignored skill and renders
// it as a whole-file add; a deleted one is a deleted-file entry; the default
// (git-only) call lists neither.
func TestStoryDiffCarriesGitignoredSubstrate(t *testing.T) {
	e := newSubstrateEnv(t)
	e.write("gone.md", "# gone\n")
	it := e.story("substrate slice")
	e.set(it.ID, "in_progress")
	rel := e.write("fresh.md", "# fresh skill\nsecond line\n")
	if err := os.Remove(filepath.Join(e.skillDir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	plain := e.diff(it.ID, verb.StoryDiffOpts{Patch: true})
	if containsPath(plain.Files, rel) || strings.Contains(plain.Patch, "fresh skill") {
		t.Errorf("default StoryDiff must stay git-only, got files %v\npatch:\n%s", plain.Files, plain.Patch)
	}

	res := e.diff(it.ID, verb.StoryDiffOpts{Patch: true, IncludeSubstrate: true})
	if !containsPath(res.Files, rel) || !containsPath(res.Files, ".satelle/skills/gone.md") {
		t.Fatalf("files must list the new and the deleted substrate path: %v", res.Files)
	}
	for _, want := range []string{
		"# substrate (untracked; whole file shown)",
		"--- /dev/null\n+++ b/" + rel + "\n",
		"+# fresh skill\n", "+second line\n",
		"deleted file .satelle/skills/gone.md",
	} {
		if !strings.Contains(res.Patch, want) {
			t.Errorf("patch missing %q:\n%s", want, res.Patch)
		}
	}
	if !strings.Contains(res.Stat, "# substrate (untracked):") || !strings.Contains(res.Stat, rel) {
		t.Errorf("stat must list the substrate path:\n%s", res.Stat)
	}
	if strings.Contains(res.Patch, "shared-attribution") {
		t.Errorf("a solo story carries no shared-attribution label:\n%s", res.Patch)
	}
}

// AC4: once a story is past its terminal edge, a substrate file another story
// edits afterwards is not attributed to it, while its own file still is.
func TestClosedStoryOmitsSubstrateEditedAfterClose(t *testing.T) {
	e := newSubstrateEnv(t)
	a := e.story("closes first")
	e.set(a.ID, "in_progress")
	own := e.write("a-own.md", "# a own\n")
	e.set(a.ID, "done")

	b := e.story("edits after")
	e.set(b.ID, "in_progress")
	later := e.write("b-later.md", "# b later\n")

	res := e.diff(a.ID, verb.StoryDiffOpts{Patch: true, IncludeSubstrate: true})
	if !containsPath(res.Files, own) {
		t.Errorf("a closed story keeps its own recorded substrate: %v", res.Files)
	}
	if containsPath(res.Files, later) || strings.Contains(res.Patch, "b later") {
		t.Errorf("a closed story must not be handed a later story's edit: %v\n%s", res.Files, res.Patch)
	}
	if !strings.Contains(res.Patch, "+# a own\n") || !strings.Contains(res.Patch, "may postdate this story's close") {
		t.Errorf("the closed story's content is labelled as current content:\n%s", res.Patch)
	}

	var viaVerb struct {
		Files []string `json:"files"`
	}
	json.Unmarshal(call(t, "story-diff", map[string]any{"id": a.ID, "include_substrate": true}), &viaVerb)
	if containsPath(viaVerb.Files, later) || !containsPath(viaVerb.Files, own) {
		t.Errorf("story-diff --include-substrate on a closed story: %v", viaVerb.Files)
	}

	// The still-open story sees its own edit through the live walk.
	if live := e.diff(b.ID, verb.StoryDiffOpts{IncludeSubstrate: true}); !containsPath(live.Files, later) {
		t.Errorf("an open story must still see its own substrate edit: %v", live.Files)
	}
}

// AC7: the reviewer is told when another story's engagement overlaps the judged
// one, because mtime cannot say which of them wrote a substrate file.
func TestSharedAttributionLabel(t *testing.T) {
	const label = "shared-attribution: also in flight "

	// engagedA engages the judged story and writes its substrate file.
	engagedA := func(t *testing.T) (*substrateEnv, workitem.Item, workitem.Item) {
		e := newSubstrateEnv(t)
		a := e.story("judged")
		b := e.story("sibling")
		e.set(a.ID, "in_progress")
		e.write("a.md", "# a\n")
		return e, a, b
	}
	patchOf := func(e *substrateEnv, id string) string {
		return e.diff(id, verb.StoryDiffOpts{Patch: true, IncludeSubstrate: true}).Patch
	}
	// A sibling's engagement, recorded the way a real one is: a baseline, a
	// change_record, and a closing transition, stamped just after A's baseline.
	releasedSibling := func(e *substrateEnv, a, b workitem.Item) {
		base := e.baselineAt(a.ID)
		e.append(b.ID, ledger.KindEngagementBaseline, "in_progress", base.Add(1*time.Microsecond))
		e.append(b.ID, ledger.KindChangeRecord, "done", base.Add(2*time.Microsecond))
		e.append(b.ID, ledger.KindStatusTransition, "done", base.Add(3*time.Microsecond))
	}

	t.Run("sibling still performing", func(t *testing.T) {
		e, a, b := engagedA(t)
		e.set(b.ID, "in_progress")
		if p := patchOf(e, a.ID); !strings.Contains(p, label+b.ID) {
			t.Errorf("a performing sibling must be named:\n%s", p)
		}
	})

	t.Run("sibling closed", func(t *testing.T) {
		e, a, b := engagedA(t)
		releasedSibling(e, a, b)
		if isPerforming(t, b.ID) {
			t.Fatal("the closed sibling must not be performing")
		}
		if p := patchOf(e, a.ID); !strings.Contains(p, label+b.ID) {
			t.Errorf("a closed sibling's overlap is read from the ledger:\n%s", p)
		}
	})

	t.Run("older ledger rows beyond the list cap", func(t *testing.T) {
		e, a, b := engagedA(t)
		releasedSibling(e, a, b)
		// More rows than ledger.List will return, all older than the window: a
		// capped oldest-first read returns only these and never reaches the sibling.
		old := e.baselineAt(a.ID).Add(-time.Hour)
		filler := e.story("filler")
		for i := 0; i < 2100; i++ {
			e.append(filler.ID, ledger.KindStatusTransition, "backlog", old.Add(time.Duration(i)*time.Millisecond))
		}
		p := patchOf(e, a.ID)
		if !strings.Contains(p, label+b.ID) {
			t.Errorf("the sibling must be found past %d older rows:\n%s", 2100, p)
		}
		if strings.Contains(p, filler.ID) {
			t.Errorf("rows older than the window are not an overlap:\n%s", p)
		}
	})

	t.Run("solo story", func(t *testing.T) {
		e, a, _ := engagedA(t)
		if p := patchOf(e, a.ID); strings.Contains(p, "shared-attribution") {
			t.Errorf("a solo story carries no label:\n%s", p)
		}
	})

	t.Run("sibling closed before the judged engagement", func(t *testing.T) {
		e, a, b := engagedA(t)
		old := e.baselineAt(a.ID).Add(-time.Hour)
		e.append(b.ID, ledger.KindEngagementBaseline, "in_progress", old)
		e.append(b.ID, ledger.KindChangeRecord, "done", old.Add(time.Second))
		e.append(b.ID, ledger.KindStatusTransition, "done", old.Add(2*time.Second))
		if p := patchOf(e, a.ID); strings.Contains(p, "shared-attribution") {
			t.Errorf("a sibling that finished before the window is not concurrent:\n%s", p)
		}
	})
}
