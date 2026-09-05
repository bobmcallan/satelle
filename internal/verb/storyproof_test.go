package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func proofWorkflow() map[string]string {
	return routeHalves(
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
`)
}

func setupProofRepo(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
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
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	wireWithWorkflows(t, proofWorkflow())
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: false}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })
	return dir
}

func createProofStory(t *testing.T) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "proof slice", "body": "goal for story-proof enumeration",
		"acceptance_criteria": "1. tests since baseline are listed",
		"category":            "feature",
		"tags":                []string{"workflow:eng"},
	}), &it)
	return it
}

func decodeProof(t *testing.T, raw json.RawMessage) verb.StoryProofResult {
	t.Helper()
	var res verb.StoryProofResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return res
}

func TestStoryProofNoBaseline(t *testing.T) {
	setupProofRepo(t)
	it := createProofStory(t)
	raw, err := dispatchRaw(t, "story-proof", map[string]any{"id": it.ID})
	if err != nil {
		t.Fatalf("no-baseline must not error: %v", err)
	}
	res := decodeProof(t, raw)
	if res.State != "no_baseline" {
		t.Errorf("state=%q want no_baseline", res.State)
	}
	if res.Tests == nil || len(res.Tests) != 0 {
		t.Errorf("tests must be empty non-nil, got %#v", res.Tests)
	}
	if strings.Contains(string(raw), `"pass"`) || strings.Contains(string(raw), `"fail"`) {
		t.Error("result must not carry a pass/fail field")
	}
}

func TestStoryProofNoNewTests(t *testing.T) {
	setupProofRepo(t)
	it := createProofStory(t)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if err := os.WriteFile(filepath.Join("a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := dispatchRaw(t, "story-proof", map[string]any{"id": it.ID})
	if err != nil {
		t.Fatalf("ok empty tests must not error: %v", err)
	}
	res := decodeProof(t, raw)
	if res.State != "ok" {
		t.Errorf("state=%q want ok (%s)", res.State, res.Note)
	}
	if res.Baseline == "" {
		t.Error("ok path must report baseline sha")
	}
	if res.Head == "" {
		t.Error("ok path must report HEAD")
	}
	if !res.Dirty {
		t.Error("uncommitted edit must report dirty=true")
	}
	if res.Tests == nil || len(res.Tests) != 0 {
		t.Errorf("tests must be empty, got %#v", res.Tests)
	}
	found := false
	for _, p := range res.NonTestFiles {
		if p == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("non_test_files should include a.txt: %v", res.NonTestFiles)
	}
}

func TestStoryProofListsNewGoTest(t *testing.T) {
	setupProofRepo(t)
	it := createProofStory(t)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	src := "package p\n\nfunc TestX(t *testing.T) {}\nfunc TestY(t *testing.T) {}\n"
	if err := os.WriteFile("foo_test.go", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := dispatchRaw(t, "story-proof", map[string]any{"id": it.ID})
	if err != nil {
		t.Fatal(err)
	}
	res := decodeProof(t, raw)
	if res.State != "ok" {
		t.Fatalf("state=%q note=%s", res.State, res.Note)
	}
	if res.Baseline == "" || res.Head == "" {
		t.Errorf("baseline=%q head=%q", res.Baseline, res.Head)
	}
	if !res.Dirty {
		t.Error("untracked test file must report dirty=true")
	}
	if len(res.Tests) != 1 || res.Tests[0].Path != "foo_test.go" || res.Tests[0].Language != "go" {
		t.Fatalf("tests=%#v", res.Tests)
	}
	got := strings.Join(res.Tests[0].Functions, ",")
	if !strings.Contains(got, "TestX") || !strings.Contains(got, "TestY") {
		t.Errorf("functions=%v", res.Tests[0].Functions)
	}
}

func TestStoryProofSkipsUnparseableLanguage(t *testing.T) {
	setupProofRepo(t)
	it := createProofStory(t)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &it)
	if err := os.MkdirAll("spec", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("spec", "thing_spec.rb"), []byte("describe 'x' do\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := dispatchRaw(t, "story-proof", map[string]any{"id": it.ID})
	if err != nil {
		t.Fatal(err)
	}
	res := decodeProof(t, raw)
	if res.State != "ok" {
		t.Fatalf("state=%q", res.State)
	}
	if len(res.Skipped) == 0 {
		t.Fatalf("want skipped entry, got tests=%#v skipped=%#v", res.Tests, res.Skipped)
	}
	if res.Skipped[0].Reason == "" {
		t.Error("skipped reason empty")
	}
}

func TestStoryProofNonStory(t *testing.T) {
	setupProofRepo(t)
	var tk workitem.Item
	json.Unmarshal(call(t, "task-create", map[string]any{
		"title": "a task", "body": "not a story",
		"acceptance_criteria": "1. x",
		"category":            "task",
	}), &tk)
	_, err := dispatchRaw(t, "story-proof", map[string]any{"id": tk.ID})
	if err == nil {
		t.Fatal("want error for non-story")
	}
	if !strings.Contains(err.Error(), "not a story") {
		t.Errorf("error=%v", err)
	}
}

func TestStoryProofStdinID(t *testing.T) {
	setupProofRepo(t)
	it := createProofStory(t)
	raw, err := dispatchRaw(t, "story-proof", map[string]any{
		"story": map[string]any{"id": it.ID},
		"from":  "backlog",
		"to":    "in_progress",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := decodeProof(t, raw)
	if res.StoryID != it.ID {
		t.Errorf("story_id=%q", res.StoryID)
	}
	if res.State != "no_baseline" {
		t.Errorf("state=%q", res.State)
	}
}
