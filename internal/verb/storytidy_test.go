package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// tidyWF is a chain of performing states so the verb can be exercised at each.
var tidyWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "integrated", "released", "closed"]
park = { state = "blocked" }
`,
	`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[integrated]
status = "integration"
agent = "executor"
requires = ["coded"]

[released]
status = "release"
agent = "executor"
requires = ["integrated"]

[closed]
status = "done"
terminal = true
requires = ["released"]
`)

// tidyRepo is a git repo (one committed file, tracked.txt) that is the test's
// cwd, wired with the tidy workflow.
func tidyRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	writeFile(t, filepath.Join(dir, "tracked.txt"), "t\n")
	run("git", "add", "tracked.txt")
	run("git", "commit", "-m", "init")
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	wireWithWorkflows(t, tidyWF)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })
	return dir
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// engageTidyStory creates a story and moves it to in_progress (recording the
// engagement baseline), then waits out filesystem timestamp granularity so
// files written next are unambiguously after the baseline.
func engageTidyStory(t *testing.T, dir string) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "tidy slice", "body": "goal", "acceptance_criteria": "1. x",
		"category": "feature", "tags": []string{"workflow:tidy"},
	}), &it)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	t.Cleanup(func() { _ = os.RemoveAll(config.StoryScratchDir(dir, it.ID)) })
	time.Sleep(30 * time.Millisecond)
	return it
}

func tidy(t *testing.T, id string, root string, paths ...string) (verb.TidyResult, error) {
	t.Helper()
	return dispatchTidy(t, "story-tidy", map[string]any{"id": id, "paths": paths, "repo_root": root})
}

func dispatchTidy(t *testing.T, name string, req map[string]any) (verb.TidyResult, error) {
	t.Helper()
	raw, err := dispatchRaw(t, name, req)
	var res verb.TidyResult
	if err == nil {
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			t.Fatal(jerr)
		}
	}
	return res, err
}

func tidyRows(t *testing.T, id, kind string) []ledger.Entry {
	t.Helper()
	var rows []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id, "kind": kind}), &rows)
	return rows
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// AC1: tidy moves a file and a directory into the story-level tidy area, one
// ledger row per path, and untidy moves them back.
func TestStoryTidyRoundTrip(t *testing.T) {
	dir := tidyRepo(t)
	it := engageTidyStory(t, dir)
	writeFile(t, filepath.Join(dir, ".ac-evidence.txt"), "debris\n")
	writeFile(t, filepath.Join(dir, "fixturegen", "sub", "a.go"), "package a\n")

	res, err := tidy(t, it.ID, dir, ".ac-evidence.txt", "fixturegen")
	if err != nil {
		t.Fatalf("tidy: %v", err)
	}
	if len(res.Moved) != 2 {
		t.Fatalf("moved=%+v", res.Moved)
	}
	tidyRoot := config.TidyDir(dir, it.ID)
	for _, rel := range []string{".ac-evidence.txt", filepath.Join("fixturegen", "sub", "a.go")} {
		if !exists(filepath.Join(tidyRoot, rel)) {
			t.Errorf("%s missing from tidy area %s", rel, tidyRoot)
		}
	}
	if exists(filepath.Join(dir, ".ac-evidence.txt")) || exists(filepath.Join(dir, "fixturegen")) {
		t.Error("files still in the tree after tidy")
	}
	rows := tidyRows(t, it.ID, ledger.KindTidy)
	if len(rows) != 2 {
		t.Fatalf("want 2 tidy rows, got %d", len(rows))
	}
	var mv struct{ Src, Dst, Action string }
	json.Unmarshal(rows[0].Payload, &mv)
	if mv.Src != filepath.Join(dir, ".ac-evidence.txt") || mv.Dst != filepath.Join(tidyRoot, ".ac-evidence.txt") || mv.Action != "tidy" {
		t.Errorf("row payload = %+v", mv)
	}

	if _, err := dispatchTidy(t, "story-untidy", map[string]any{"id": it.ID, "all": true, "repo_root": dir}); err != nil {
		t.Fatalf("untidy: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".ac-evidence.txt")); string(b) != "debris\n" {
		t.Errorf("restored content = %q", b)
	}
	if !exists(filepath.Join(dir, "fixturegen", "sub", "a.go")) {
		t.Error("directory not restored")
	}
	if n := len(tidyRows(t, it.ID, ledger.KindTidyRestore)); n != 2 {
		t.Errorf("want 2 restore rows, got %d", n)
	}
	// Nothing left to restore.
	if _, err := dispatchTidy(t, "story-untidy", map[string]any{"id": it.ID, "paths": []string{".ac-evidence.txt"}, "repo_root": dir}); err == nil {
		t.Error("second untidy of an already-restored path should refuse")
	}
}

// AC1: a second tidy of the same relative path keeps both copies.
func TestStoryTidyRepeatedPathDoesNotClobber(t *testing.T) {
	dir := tidyRepo(t)
	it := engageTidyStory(t, dir)
	for _, body := range []string{"one\n", "two\n"} {
		writeFile(t, filepath.Join(dir, "stray.txt"), body)
		if _, err := tidy(t, it.ID, dir, "stray.txt"); err != nil {
			t.Fatal(err)
		}
	}
	root := config.TidyDir(dir, it.ID)
	a, _ := os.ReadFile(filepath.Join(root, "stray.txt"))
	b, _ := os.ReadFile(filepath.Join(root, "stray.txt.1"))
	if string(a) != "one\n" || string(b) != "two\n" {
		t.Errorf("first=%q second=%q", a, b)
	}
}

// AC1: untidy never overwrites a file that has reappeared at the source.
func TestStoryUntidyRefusesToOverwrite(t *testing.T) {
	dir := tidyRepo(t)
	it := engageTidyStory(t, dir)
	writeFile(t, filepath.Join(dir, "stray.txt"), "old\n")
	if _, err := tidy(t, it.ID, dir, "stray.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "stray.txt"), "new\n")
	_, err := dispatchTidy(t, "story-untidy", map[string]any{"id": it.ID, "all": true, "repo_root": dir})
	if err == nil || !strings.Contains(err.Error(), "will not overwrite") {
		t.Fatalf("want overwrite refusal, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stray.txt")); string(b) != "new\n" {
		t.Errorf("file overwritten: %q", b)
	}
}

// AC2: every ineligible path is refused with its reason, nothing is touched,
// and no tidy ledger row is written.
func TestStoryTidyRefusals(t *testing.T) {
	dir := tidyRepo(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	writeFile(t, outside, "x\n")

	// Untracked before the story engaged: predates the baseline.
	writeFile(t, filepath.Join(dir, "preexisting.txt"), "p\n")
	// Committed, then removed from the index: untracked but present in HEAD.
	writeFile(t, filepath.Join(dir, "inhead.txt"), "h\n")
	for _, args := range [][]string{{"add", "inhead.txt"}, {"commit", "-m", "add inhead"}, {"rm", "--cached", "inhead.txt"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	time.Sleep(30 * time.Millisecond)
	it := engageTidyStory(t, dir)
	writeFile(t, filepath.Join(dir, "fresh.txt"), "f\n")

	cases := []struct{ name, path, reason string }{
		{"tracked", "tracked.txt", "tracked"},
		{"in HEAD", "inhead.txt", "exists in HEAD"},
		{"predates baseline", "preexisting.txt", "predates engagement baseline"},
		{"outside absolute", outside, "outside worktree"},
		{"outside dotdot", "../" + filepath.Base(filepath.Dir(outside)) + "/elsewhere.txt", "outside worktree"},
		{"inside .git", ".git/HEAD", "outside worktree"},
		{"missing", "nope.txt", "not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tidy(t, it.ID, dir, c.path)
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("want refusal containing %q, got %v", c.reason, err)
			}
			if n := len(tidyRows(t, it.ID, ledger.KindTidy)); n != 0 {
				t.Errorf("%d tidy rows written by a refusal", n)
			}
		})
	}
	for _, p := range []string{filepath.Join(dir, "tracked.txt"), filepath.Join(dir, "inhead.txt"), filepath.Join(dir, "preexisting.txt"), outside, filepath.Join(dir, ".git", "HEAD")} {
		if !exists(p) {
			t.Errorf("%s was touched by a refused tidy", p)
		}
	}

	// All-or-nothing: one good path beside a bad one moves neither.
	if _, err := tidy(t, it.ID, dir, "fresh.txt", "tracked.txt"); err == nil {
		t.Fatal("mixed tidy should refuse")
	}
	if !exists(filepath.Join(dir, "fresh.txt")) {
		t.Error("the eligible path moved despite a sibling refusal")
	}
}

// AC3: tidy works at every performing step, and refuses an unengaged story.
func TestStoryTidyAtEveryPerformingStep(t *testing.T) {
	dir := tidyRepo(t)
	it := engageTidyStory(t, dir)
	for _, status := range []string{"in_progress", "integration", "release"} {
		if status != "in_progress" {
			json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": status}), &it)
		}
		name := "stray-" + status + ".txt"
		writeFile(t, filepath.Join(dir, name), "s\n")
		if _, err := tidy(t, it.ID, dir, name); err != nil {
			t.Errorf("tidy at %s: %v", status, err)
		}
		if exists(filepath.Join(dir, name)) {
			t.Errorf("%s not moved at %s", name, status)
		}
	}

	var fresh workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "unengaged", "body": "goal", "acceptance_criteria": "1. x",
		"category": "feature", "tags": []string{"workflow:tidy"},
	}), &fresh)
	writeFile(t, filepath.Join(dir, "late.txt"), "l\n")
	if _, err := tidy(t, fresh.ID, dir, "late.txt"); err == nil {
		t.Error("tidy on a story that was never engaged should refuse")
	}
}
