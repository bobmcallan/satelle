//go:build integration

package tests

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
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// gateWorktreeRepo builds a repo whose `done` edge is gated by the shipped
// satelle-substrate-only-check — a functional check that enumerates the story's
// change set through `satelle story diff` (sty_ab93f9a6). The substrate is
// committed so a second working tree is governed too.
func gateWorktreeRepo(t *testing.T) (repo, tree string) {
	t.Helper()
	return gateWorktreeRepoWith(t, "satelle-substrate-only-check")
}

// diffProbeCheck is a functional check that always rejects and prints the change
// set `story diff` hands a gate — so a test can read what the check SAW.
const diffProbeCheck = `---
name: gate-diff-probe
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Test probe — rejects after printing the live change set the gate's check sees.
---

# Gate diff probe

` + "```check" + `
sid=$(grep -oE 'sty_[a-f0-9]+' | head -1)
satelle story diff "$sid" --include-substrate
echo "probe-cwd=$(pwd)"
exit 1
` + "```" + `
`

func gateWorktreeRepoWith(t *testing.T, reviewer string) (repo, tree string) {
	t.Helper()
	repo = t.TempDir()
	gitInitRepo(t, repo)
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "gate-diff-probe.md"), diffProbeCheck)
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor|||", "done|||"+reviewer+"|")
	mustRun(t, testBin, repo, "reindex")
	tree = addWorktree(t, repo)
	return repo, tree
}

// presentDone presents id's close from dir. The gate's check shells out to a bare
// `satelle`, so the binary under test leads PATH — otherwise the check would run
// whatever satelle is installed and prove nothing about this tree.
func presentDone(t *testing.T, dir, id string) (string, error) {
	t.Helper()
	path := "PATH=" + filepath.Dir(testBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	return runEnv(t, testBin, dir, []string{path}, "story", "set", id, "--status", "done")
}

func gateStory(t *testing.T, repo, engageFrom string) string {
	t.Helper()
	id := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "gate tree",
		"--body", "goal", "--acceptance", "1. judged", "--category", "chore"), "sty_")
	if id == "" {
		t.Fatal("no story id")
	}
	mustRun(t, testBin, engageFrom, "story", "set", id, "--status", "in_progress")
	return id
}

// TestGateCheckSeesWorktreeEngagedChangeSet (sty_ab93f9a6 AC1): the gate runs the
// check in the MAIN tree, yet a story engaged from a linked worktree whose only
// edit is main-tree substrate is judged on that edit — accepted — and the check
// names the file. Run through the engine (`story set --status done`), not the
// script by hand.
func TestGateCheckSeesWorktreeEngagedChangeSet(t *testing.T) {
	repo, tree := gateWorktreeRepo(t)
	id := gateStory(t, repo, tree)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "gate-wt.md"), "# s\n")

	// Presented from the main tree, as the driving session does: the gate's check
	// then runs in the main tree while the story is anchored to the worktree.
	out, err := presentDone(t, repo, id)
	if err != nil {
		t.Fatalf("a worktree-engaged substrate-only story must be accepted: %v\n%s", err, out)
	}
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "done"`) {
		t.Fatalf("story is not done:\n%s", got)
	}
	// The same enumeration, from the story's own tree, lists the file.
	diff := mustRun(t, testBin, tree, "story", "diff", id, "--include-substrate")
	if !strings.Contains(diff, ".satelle/skills/gate-wt.md") {
		t.Errorf("story diff from the worktree should list the main-tree skill:\n%s", diff)
	}
}

// TestGateCheckProbeReadsWorktreeChangeSetFromMainTree (AC1 + AC4): what the
// check SEES, read straight off a probe — the worktree story's change set,
// including the main-tree substrate edit — while its cwd is the main tree.
func TestGateCheckProbeReadsWorktreeChangeSetFromMainTree(t *testing.T) {
	repo, tree := gateWorktreeRepoWith(t, "gate-diff-probe")
	id := gateStory(t, repo, tree)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "gate-probe.md"), "# s\n")
	out, err := presentDone(t, repo, id)
	if err == nil {
		t.Fatalf("the probe always rejects:\n%s", out)
	}
	if !strings.Contains(out, ".satelle/skills/gate-probe.md") {
		t.Errorf("the check must see the worktree story's change set:\n%s", out)
	}
	if strings.Contains(out, "engaged from working tree") {
		t.Errorf("the check must not hit the foreign-tree refusal:\n%s", out)
	}
	want, _ := filepath.EvalSymlinks(repo)
	got := out
	if !strings.Contains(got, "probe-cwd="+repo) && !strings.Contains(got, "probe-cwd="+want) {
		t.Errorf("the check's cwd must stay the main tree %s:\n%s", repo, out)
	}
}

// A worktree-engaged story with a CODE change is still judged, not waved through:
// resolving the story's tree must not blind the check.
func TestGateCheckRejectsWorktreeEngagedCodeChange(t *testing.T) {
	repo, tree := gateWorktreeRepo(t)
	id := gateStory(t, repo, tree)
	if err := os.WriteFile(filepath.Join(tree, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := presentDone(t, repo, id)
	if err == nil {
		t.Fatalf("a code change must be rejected as not substrate-only:\n%s", out)
	}
	if !strings.Contains(out, "seed.txt") {
		t.Errorf("rejection should name the offending path:\n%s", out)
	}
}

// TestGateCheckMainTreeEngagedUnchanged (AC2): a story engaged from the main tree
// sees the same change set as before — accepted on substrate, rejected on code.
func TestGateCheckMainTreeEngagedUnchanged(t *testing.T) {
	t.Run("substrate accepted", func(t *testing.T) {
		repo, _ := gateWorktreeRepo(t)
		id := gateStory(t, repo, repo)
		writeFile(t, filepath.Join(repo, ".satelle", "skills", "gate-main.md"), "# s\n")
		if out, err := presentDone(t, repo, id); err != nil {
			t.Fatalf("main-tree substrate-only story must be accepted: %v\n%s", err, out)
		}
	})
	t.Run("code rejected", func(t *testing.T) {
		repo, _ := gateWorktreeRepo(t)
		id := gateStory(t, repo, repo)
		if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := presentDone(t, repo, id)
		if err == nil {
			t.Fatalf("a code change must be rejected:\n%s", out)
		}
		if !strings.Contains(out, "seed.txt") {
			t.Errorf("rejection should name the offending path:\n%s", out)
		}
	})
}

// legacyParkRepo is the id4me sty_d9904629 shape (sty_5a6c06d3): a substrate-lane
// story in a clone of a remote trunk commits under its own id, another story's
// commit lands Makefile, README.md and .version on the trunk and is fast-forwarded
// in, and the story parks and resumes. The park's change_record is the shape a
// binary before sty_659c4133 wrote, listing every file in its range; a current
// binary never writes it, so the test appends it to the story's ledger itself.
func legacyParkRepo(t *testing.T) (repo, id, head string) {
	t.Helper()
	r := testutil.NewTrunkRepos(t)
	r = r.Adopt(t, t.TempDir())
	repo = r.Subject
	mustRun(t, testBin, repo, "init")
	// init's scaffold leaves the clone's trunk dirty, which the engage-time trunk
	// check refuses; the remote trunk the recorded channel reads is unaffected.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[trunk]\ncheck = false\n")
	writeSpineFixture(t, repo, "blocked", "", "",
		"in_progress|executor|||", "done|||satelle-substrate-only-check|")
	mustRun(t, testBin, repo, "reindex")
	id = gateStory(t, repo, repo)
	base := r.Head(t, repo)

	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "docs", "legacy.md"), "# legacy\n")
	r.Git(t, repo, "add", "docs/legacy.md")
	r.Git(t, repo, "commit", "--quiet", "-m", "docs: legacy ("+id+")")
	r.Git(t, repo, "push", "--quiet", "origin", "main")
	foreign := []string{"Makefile", "README.md", ".version"}
	r.Git(t, r.Pusher, "pull", "--quiet", "--ff-only")
	for _, f := range foreign {
		writeFile(t, filepath.Join(r.Pusher, f), f+"\n")
	}
	r.Git(t, r.Pusher, append([]string{"add"}, foreign...)...)
	r.Git(t, r.Pusher, "commit", "--quiet", "-m", "ci: proven tree (sty_0the0059)")
	r.Git(t, r.Pusher, "push", "--quiet", "origin", "main")
	r.Git(t, repo, "pull", "--quiet", "--ff-only")

	mustRun(t, testBin, repo, "story", "set", id, "--status", "blocked")
	head = r.Head(t, repo)
	appendChangeRecord(t, repo, id, map[string]any{
		"from": "in_progress", "to": "blocked", "since_sha": base, "head_sha": head,
		"files": append([]string{"docs/legacy.md"}, foreign...), "file_count": 4,
	})
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	return repo, id, head
}

// appendChangeRecord appends a change_record row with payload to id's ledger in
// repo's runtime store and returns the row's entry id.
func appendChangeRecord(t *testing.T, repo, id string, payload map[string]any) string {
	t.Helper()
	db, err := store.Open(filepath.Join(runtimeRoot(t, repo), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, _ := json.Marshal(payload)
	e, err := db.Ledger.Append(context.Background(), ledger.AppendInput{
		StoryID: id, Kind: ledger.KindChangeRecord, Actor: "executor", Body: "legacy change record", Payload: raw,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

// TestSubstrateOnlyCheckAcceptsLegacyParkRecord (sty_5a6c06d3 AC2): the close of
// the id4me shape is accepted through the real gate, because the legacy park row
// no longer carries the other story's trunk files when it is read.
func TestSubstrateOnlyCheckAcceptsLegacyParkRecord(t *testing.T) {
	repo, id, _ := legacyParkRepo(t)
	if rec := mustRun(t, testBin, repo, "story", "diff", id, "--recorded"); strings.Contains(rec, "Makefile") ||
		!strings.Contains(rec, "docs/legacy.md") {
		t.Errorf("recorded change set must keep docs/legacy.md and drop the trunk files:\n%s", rec)
	}
	out, err := presentDone(t, repo, id)
	if err != nil {
		t.Fatalf("a substrate-only story with a legacy park record must be accepted: %v\n%s", err, out)
	}
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "done"`) {
		t.Fatalf("story is not done:\n%s", got)
	}
}

// TestSubstrateOnlyCheckNamesUnresolvedRecord (sty_5a6c06d3 AC3): a recorded row
// whose range git cannot show is used as recorded and the real check names it.
func TestSubstrateOnlyCheckNamesUnresolvedRecord(t *testing.T) {
	repo, id, head := legacyParkRepo(t)
	entry := appendChangeRecord(t, repo, id, map[string]any{
		"from": "in_progress", "to": "blocked", "since_sha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"head_sha": head, "files": []string{"docs/unresolved.md"}, "file_count": 1,
	})
	materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "skills", "satelle-substrate-only-check.md"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(script, []byte(structure.CheckFence(string(body))+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(`{"story":{"id":"` + id + `"},"from":"in_progress","to":"done"}`)
	cmd.Env = append(isolatedEnv(t), withTestBinOnPATH(t)...)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("the check must accept a lane-only slice: %v\n%s", err, out)
	}
	if !strings.Contains(out, "recorded rows used as recorded (range unresolved):") || !strings.Contains(out, entry) {
		t.Errorf("the check must name the unresolved row %s:\n%s", entry, out)
	}
	if !strings.Contains(out, "docs/unresolved.md") {
		t.Errorf("the unresolved row's files must be used as recorded:\n%s", out)
	}
}

// TestStoryDiffForeignTreeStillRefusedOutsideGate (AC3, end to end): with no gate
// context the main tree still may not diff a worktree-engaged story.
func TestStoryDiffForeignTreeStillRefusedOutsideGate(t *testing.T) {
	repo, tree := gateWorktreeRepo(t)
	id := gateStory(t, repo, tree)
	out, err := run(t, testBin, repo, "story", "diff", id)
	if err == nil || !strings.Contains(out, "was engaged from working tree") {
		t.Fatalf("a foreign-tree diff must be refused (err=%v):\n%s", err, out)
	}
}
