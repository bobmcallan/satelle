//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sty_36b91fd1 AC4: a repo whose overlay puts a readiness step and a release on
// `*`, under the shipped epic-parent readiness step (which declares a mark and
// `advise` with satelle-workflow-advisor), is prompted to run the advisor on its
// first epic readiness, and only then.

const advisorPromptLine = "advise: consult the satelle-workflow-advisor skill"

// advisedOverlayDone puts a readiness and a release obligation on every working
// story; epic-parent is left to the shipped table.
const advisedOverlayDone = `["*"]
obligations = ["raised", "readied", "coded", "released", "closed"]
`

const advisedOverlayStep = `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "child-planner"
skills = ["child-plan"]
propose = true
mark = "readied"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["readied"]

[released]
status = "release"
agent = "executor"
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["released"]
`

const advisedReadyStub = `#!/bin/sh
cat > /dev/null
echo '{"artifact":{"name":"ready","type":"review","body":"parallel: all; edges: none; lanes: none"},"changes":[]}'
`

const advisedPlannerStub = `#!/bin/sh
cat > /dev/null
echo '{"decision":"accept","notes":""}'
`

// advisedRepo is the overlay repo with stub performers: the shipped
// ready-reviewer reports no repairs, the child planner accepts.
func advisedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	writeRouteFixture(t, repo, advisedOverlayDone, advisedOverlayStep)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "child-plan.md"), fmt.Sprintf(changesSkillFixture, "child-plan", "child-plan"))

	stubDir := t.TempDir()
	ready := filepath.Join(stubDir, "pi")
	planner := filepath.Join(stubDir, "planner")
	for path, body := range map[string]string{ready: advisedReadyStub, planner: advisedPlannerStub} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), fmt.Sprintf(
		"[ready-reviewer]\ncommand = %q\ntools = \"read,grep,find,ls\"\nmodel = \"stub-model\"\n\n"+
			"[child-planner]\ncommand = %q\ntools = \"read,grep,find,ls,read_file\"\nmodel = \"stub-model\"\n",
		ready+" -p --tools {tools} {system}", planner+" -p --tools {tools} {system}"))
	mustRun(t, testBin, repo, "reindex")
	return repo
}

func advisedCreate(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out := mustRun(t, testBin, repo, append([]string{"story", "create"}, args...)...)
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	return id
}

// advisedEpic files an epic-parent and one feature child of its set.
func advisedEpic(t *testing.T, repo, theme string) (epic, child string) {
	t.Helper()
	epic = advisedCreate(t, repo, "--category", "epic-parent", "--title", "Epic "+theme, "--body", "umbrella", "--tags", "epic:"+theme)
	child = advisedCreate(t, repo, "--category", "feature", "--title", "Child "+theme, "--body", "c", "--acceptance", "1. c", "--tags", "epic:"+theme)
	return epic, child
}

func wantMarked(t *testing.T, repo, id, mark string) {
	t.Helper()
	var s struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, testBin, repo, "story", "get", id)), &s); err != nil {
		t.Fatalf("story get %s: %v", id, err)
	}
	if !carriesTag(s.Tags, mark) {
		t.Errorf("%s must carry the %q mark, has %v", id, mark, s.Tags)
	}
}

// advisedWorkflows snapshots the repo's workflow files.
func advisedWorkflows(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	dir := filepath.Join(repo, ".satelle", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func TestFirstEpicReadinessPromptsTheWorkflowAdvisor(t *testing.T) {
	repo := advisedRepo(t)
	if out := mustRun(t, testBin, repo, "validate"); strings.Count(out, advisorPromptLine) != 1 {
		t.Fatalf("a passing validate prompts once:\n%s", out)
	}
	before := advisedWorkflows(t, repo)

	epic, child := advisedEpic(t, repo, "one")
	out := mustRun(t, testBin, repo, "story", "ready", epic)
	if !strings.Contains(out, epic+" marked") {
		t.Fatalf("the epic must be readied:\n%s", out)
	}
	wantMarked(t, repo, child, "readied")
	wantMarked(t, repo, epic, "ready")
	if n := strings.Count(out, advisorPromptLine); n != 1 {
		t.Fatalf("the first epic readiness prompts once, got %d:\n%s", n, out)
	}

	out = mustRun(t, testBin, repo, "story", "ready", epic)
	wantMarked(t, repo, epic, "ready")
	if strings.Contains(out, advisorPromptLine) {
		t.Errorf("readying the same epic again does not prompt:\n%s", out)
	}

	out = mustRun(t, testBin, repo, "story", "ready", child, "--json")
	if !strings.Contains(out, `"reused": true`) && !strings.Contains(out, `"reused":true`) {
		t.Fatalf("the child's held readiness must be reused:\n%s", out)
	}
	if out := mustRun(t, testBin, repo, "story", "ready", child); strings.Contains(out, advisorPromptLine) {
		t.Errorf("a reused readiness round does not prompt:\n%s", out)
	}

	second, secondChild := advisedEpic(t, repo, "two")
	out = mustRun(t, testBin, repo, "story", "ready", second)
	wantMarked(t, repo, secondChild, "readied")
	wantMarked(t, repo, second, "ready")
	if strings.Contains(out, advisorPromptLine) {
		t.Errorf("a second epic does not prompt:\n%s", out)
	}

	after := advisedWorkflows(t, repo)
	if len(after) != len(before) {
		t.Errorf("the workflow file set changed: %d files before, %d after", len(before), len(after))
	}
	for name, body := range before {
		if after[name] != body {
			t.Errorf("workflows/%s changed while only printing a prompt", name)
		}
	}

	rubric := mustRun(t, testBin, repo, "doc", "get", "skills", "satelle-workflow-advisor")
	if !strings.Contains(rubric, "epic-branch lane") {
		t.Errorf("the advisor resolved in this repo must name the epic-branch lane:\n%s", rubric)
	}
}

// The same prompt on the other readiness command, and never with --json.
func TestFirstEpicReadinessByStorySetPromptsTheWorkflowAdvisor(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		repo := advisedRepo(t)
		epic, _ := advisedEpic(t, repo, "one")
		out := mustRun(t, testBin, repo, "story", "set", epic, "--status", "ready")
		if n := strings.Count(out, advisorPromptLine); n != 1 {
			t.Fatalf("story set on the first epic prompts once, got %d:\n%s", n, out)
		}
		second, _ := advisedEpic(t, repo, "two")
		if out := mustRun(t, testBin, repo, "story", "set", second, "--status", "ready"); strings.Contains(out, advisorPromptLine) {
			t.Errorf("a second epic does not prompt:\n%s", out)
		}
	})
	t.Run("story set --json", func(t *testing.T) {
		repo := advisedRepo(t)
		epic, _ := advisedEpic(t, repo, "one")
		out := mustRun(t, testBin, repo, "story", "set", epic, "--status", "ready", "--json")
		if strings.Contains(out, advisorPromptLine) || !strings.Contains(out, `"advise"`) {
			t.Errorf("--json carries the advice as a field and prints no prompt line:\n%s", out)
		}
	})
	t.Run("story ready --json", func(t *testing.T) {
		repo := advisedRepo(t)
		epic, _ := advisedEpic(t, repo, "one")
		out := mustRun(t, testBin, repo, "story", "ready", epic, "--json")
		if strings.Contains(out, advisorPromptLine) || !strings.Contains(out, `"advise"`) {
			t.Errorf("--json carries the advice as a field and prints no prompt line:\n%s", out)
		}
	})
}
