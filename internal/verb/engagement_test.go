package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestEngagementBaselineIdempotentAndDiff: first engage records baseline once;
// park/re-enter keeps one row; story-diff enumerates tracked+untracked (sty_da169e03).
func TestEngagementBaselineIdempotentAndDiff(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "a.txt")
	run("git", "commit", "-m", "init")
	head, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	headSHA := strings.TrimSpace(string(head))

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	wireWithWorkflows(t, routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
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
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "baseline slice", "body": "goal for engagement baseline",
		"acceptance_criteria": "1. baseline recorded",
		"category":            "feature",
		"tags":                []string{"workflow:eng"},
	}), &it)

	if _, err := dispatchRaw(t, "story-diff", map[string]any{"id": it.ID}); err == nil {
		t.Fatal("want error with no baseline")
	} else if !strings.Contains(err.Error(), "no engagement baseline") {
		t.Errorf("error should mention no baseline: %v", err)
	}

	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if it.Status != "in_progress" {
		t.Fatalf("status=%s", it.Status)
	}

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &entries)
	if len(entries) != 1 {
		t.Fatalf("want 1 engagement_baseline, got %d: %+v", len(entries), entries)
	}
	var p struct {
		HeadSHA string `json:"head_sha"`
		To      string `json:"to"`
	}
	json.Unmarshal(entries[0].Payload, &p)
	if p.HeadSHA != headSHA {
		t.Errorf("head_sha=%q want %q", p.HeadSHA, headSHA)
	}
	if p.To != "in_progress" {
		t.Errorf("to=%q", p.To)
	}

	// Park then re-enter performing: still one baseline, original sha.
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindEngagementBaseline}), &entries)
	if len(entries) != 1 {
		t.Fatalf("after park/re-enter want still 1 baseline, got %d", len(entries))
	}
	json.Unmarshal(entries[0].Payload, &p)
	if p.HeadSHA != headSHA {
		t.Errorf("after re-enter head_sha=%q want original %q", p.HeadSHA, headSHA)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new_untracked.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := call(t, "story-diff", map[string]any{"id": it.ID})
	var res struct {
		Files    []string `json:"files"`
		Stat     string   `json:"stat"`
		Baseline string   `json:"baseline_sha"`
		Note     string   `json:"note"`
	}
	json.Unmarshal(raw, &res)
	if res.Baseline != headSHA {
		t.Errorf("diff baseline=%q", res.Baseline)
	}
	if !strings.Contains(res.Note, "enumeration only") {
		t.Errorf("note should disclaim verdict: %q", res.Note)
	}
	foundA, foundU := false, false
	for _, f := range res.Files {
		if f == "a.txt" {
			foundA = true
		}
		if f == "new_untracked.txt" {
			foundU = true
		}
	}
	if !foundA {
		t.Errorf("files should include a.txt: %v", res.Files)
	}
	if !foundU {
		t.Errorf("files should include untracked new_untracked.txt: %v", res.Files)
	}
	if res.Stat == "" {
		t.Error("empty stat")
	}

	rawStdin := call(t, "story-diff", map[string]any{
		"story": map[string]any{"id": it.ID},
		"from":  "blocked",
		"to":    "in_progress",
	})
	var resStdin struct {
		StoryID string `json:"story_id"`
	}
	json.Unmarshal(rawStdin, &resStdin)
	if resStdin.StoryID != it.ID {
		t.Errorf("stdin form story_id=%q", resStdin.StoryID)
	}

	raw2 := call(t, "story-diff", map[string]any{"id": it.ID, "patch": true})
	var res2 struct {
		Patch string `json:"patch"`
	}
	json.Unmarshal(raw2, &res2)
	if res2.Patch == "" {
		t.Error("want patch body with patch:true")
	}
}

// A story parked across another story's commit must not have that commit
// attributed to it. Covers BOTH channels the substrate-only close gate unions —
// the recorded change_record union and the live story-diff --include-substrate
// leg — plus a vacuity control proving the assertions can fail (sty_526d6a68).
func TestResumeReanchorsPastParkWindowCommits(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	sha := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	write("seed.txt", "seed\n")
	run("git", "add", "seed.txt")
	run("git", "commit", "-m", "seed")

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	wireWithWorkflows(t, routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
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
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	engage := func(title string) workitem.Item {
		t.Helper()
		var it workitem.Item
		json.Unmarshal(call(t, "story-create", map[string]any{
			"title": title, "body": "body for " + title,
			"acceptance_criteria": "1. x",
			"category":            "feature",
			"tags":                []string{"workflow:eng"},
		}), &it)
		json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
		return it
	}

	recordedFiles := func(id string) []string {
		t.Helper()
		var res struct {
			Files []string `json:"files"`
		}
		json.Unmarshal(call(t, "story-diff", map[string]any{"id": id, "recorded": true}), &res)
		return res.Files
	}
	liveFiles := func(id string) []string {
		t.Helper()
		var res struct {
			Files    []string `json:"files"`
			Baseline string   `json:"baseline"`
		}
		// Exactly the request satelle-substrate-only-check issues for channel 2.
		json.Unmarshal(call(t, "story-diff", map[string]any{"id": id, "include_substrate": true}), &res)
		return res.Files
	}
	has := func(files []string, want string) bool {
		for _, f := range files {
			if f == want {
				return true
			}
		}
		return false
	}

	// --- the parked story ---
	parked := engage("parked slice")

	// Its own committed work, before the park.
	write("mine_before.txt", "mine\n")
	run("git", "add", "mine_before.txt")
	run("git", "commit", "-m", "own work before park")

	json.Unmarshal(call(t, "story-set", map[string]any{"id": parked.ID, "status": "blocked"}), &parked)

	// Another story lands a commit while this one sits parked.
	write("foreign.txt", "not mine\n")
	run("git", "add", "foreign.txt")
	run("git", "commit", "-m", "another story's slice")
	foreignSHA := sha()

	json.Unmarshal(call(t, "story-set", map[string]any{"id": parked.ID, "status": "in_progress"}), &parked)

	// (a) the resume wrote a re-anchor row at the resume point, enumerating nothing.
	var recs []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": parked.ID, "kind": ledger.KindChangeRecord}), &recs)
	if len(recs) == 0 {
		t.Fatal("want change_record rows")
	}
	var last struct {
		HeadSHA        string   `json:"head_sha"`
		Files          []string `json:"files"`
		ReanchorResume bool     `json:"reanchor_resume"`
	}
	json.Unmarshal(recs[len(recs)-1].Payload, &last)
	if !last.ReanchorResume {
		t.Error("resume row must be marked reanchor_resume")
	}
	if len(last.Files) != 0 {
		t.Errorf("re-anchor row must enumerate nothing, got %v", last.Files)
	}
	if last.HeadSHA != foreignSHA {
		t.Errorf("re-anchor head_sha=%q want resume HEAD %q", last.HeadSHA, foreignSHA)
	}

	// Do more of its own work, then transition forward.
	write("mine_after.txt", "mine too\n")
	run("git", "add", "mine_after.txt")
	run("git", "commit", "-m", "own work after resume")
	json.Unmarshal(call(t, "story-set", map[string]any{"id": parked.ID, "status": "done"}), &parked)

	// (b)+(c) channel 1 — the recorded union the gate reads.
	rec := recordedFiles(parked.ID)
	if has(rec, "foreign.txt") {
		t.Errorf("recorded union must exclude the park-window commit: %v", rec)
	}
	if !has(rec, "mine_before.txt") {
		t.Errorf("recorded union must keep pre-park own work: %v", rec)
	}
	if !has(rec, "mine_after.txt") {
		t.Errorf("recorded union must keep post-resume own work: %v", rec)
	}

	// (d) channel 2 — the live leg the gate issues.
	live := liveFiles(parked.ID)
	if has(live, "foreign.txt") {
		t.Errorf("live --include-substrate must exclude the park-window commit: %v", live)
	}

	// (e) vacuity control: a story that NEVER parked still enumerates everything
	// committed since its own engagement, so the assertions above are not trivially
	// true of any story.
	control := engage("never parked")
	write("after_control.txt", "x\n")
	run("git", "add", "after_control.txt")
	run("git", "commit", "-m", "commit after control engaged")
	json.Unmarshal(call(t, "story-set", map[string]any{"id": control.ID, "status": "done"}), &control)
	cl := liveFiles(control.ID)
	if !has(cl, "after_control.txt") {
		t.Fatalf("control: an unparked story must still see commits since engagement: %v", cl)
	}
}
