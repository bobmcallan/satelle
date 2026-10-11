//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// writePlanDemoRoute isolates the dispatched plan step: backlog → plan(planner)
// → in_progress(in-loop) → done, with entry to in_progress gated by the real
// satelle-story-plan-review.
func writePlanDemoRoute(t *testing.T, repo string) {
	t.Helper()
	writeSpineFixture(t, repo, "", "", "",
		"plan|planner|plan||",
		"in_progress|executor||satelle-story-plan-review|reviewer",
		"done||||")
}

// planSkillFixture is the planner rubric the demo route names. The stubbed
// planner process never reads its prose; the dispatch needs it to resolve and to
// declare the structured plan output contract the tests exercise.
const planSkillFixture = `---
name: plan
scope: project
type: skill
tags: [type:skill]
description: Fixture planner rubric for the plan-dispatch tests — propose a plan for the story's acceptance criteria.
output_name: plan
output_type: plan
output_required: true
output_schema: name,type,body
output_ac_coverage: true
---

# Plan (fixture)

Read the story and propose an implementation plan that covers every acceptance criterion.
`

const validStructuredPlannerScript = `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"artifact":{"name":"plan","type":"plan","body":"# Plan\n\n## AC1\nThe thing is covered."}}'
`

func setupStructuredPlanRepo(t *testing.T, scriptBody string) (repo, id, script string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	stubReviewerAccept(t, repo)
	// The planner's skill is a minimal committed fixture, not this repo's authored
	// `plan` (untracked .satelle, absent on a clean checkout); the dispatch
	// mechanism under test needs only that the named skill resolves.
	// satelle-story-plan-review is an embedded default served by the overlay.
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "plan.md"), planSkillFixture)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "satelle-story-plan-review.md"),
		substrateSkillBody(t, "satelle-story-plan-review"))
	script = filepath.Join(repo, "planner.sh")
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(repo, ".satelle", "workflows", "agents.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(fmt.Sprintf("\n[planner]\ncommand = \"%s {system}\"\ntools = \"read_file,grep,list_dir\"\nmodel = \"fable\"\n", script)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	writePlanDemoRoute(t, repo)
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "story", "create", "--category", "plandemo",
		"--title", "Plan me", "--body", "do the thing", "--acceptance", "1. the thing is done")
	id = extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}
	return repo, id, script
}

// TestPlanStepDispatchesFableAndCapturesArtifact drives the real binary to prove
// the plan step: a read-only planner returns structured JSON and Satelle owns the
// typed artifact write before committing the transition.
func TestPlanStepDispatchesFableAndCapturesArtifact(t *testing.T) {
	repo, id, _ := setupStructuredPlanRepo(t, validStructuredPlannerScript)

	// Enter plan → the planner is dispatched and captures the plan artifact.
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	planDoc := filepath.Join(runtimeRoot(t, repo), "stories", id, "plan.md")
	if _, err := os.Stat(planDoc); err != nil {
		t.Fatalf("plan step did not capture a plan artifact under the story: %v", err)
	}
	data, err := os.ReadFile(planDoc)
	if err != nil || !strings.Contains(string(data), "The thing is covered") {
		t.Fatalf("Satelle-owned plan artifact content missing: %v\n%s", err, data)
	}

	// The plan-review gate admits the plan to in_progress.
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "in_progress"`) {
		t.Errorf("plan-review did not admit the story to in_progress:\n%s", got)
	}
	// sty_58fa970e AC3: end-to-end plan→in_progress must not recreate the
	// obsolete in-repo attachment dir.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "stories")); err == nil {
		t.Error("in-repo .satelle/stories/ recreated after plan dispatch — attachment channel regressed")
	}
}

func TestStructuredPlanFailureClearsLeaseAndRetryAttaches(t *testing.T) {
	repo, id, script := setupStructuredPlanRepo(t, "#!/bin/sh\ncat >/dev/null\necho malformed\n")
	out, err := run(t, testBin, repo, "story", "set", id, "--status", "plan")
	if err == nil || !strings.Contains(out, "no structured") {
		t.Fatalf("malformed structured result should refuse transition: err=%v\n%s", err, out)
	}
	got := mustRun(t, testBin, repo, "story", "get", id)
	if !strings.Contains(got, `"status": "backlog"`) {
		t.Fatalf("failed attachment path advanced status:\n%s", got)
	}
	if seats := mustRun(t, testBin, repo, "story", "seat"); strings.TrimSpace(seats) != "[]" {
		t.Fatalf("failed structured result left an in-flight lease:\n%s", seats)
	}
	if err := os.WriteFile(script, []byte(validStructuredPlannerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	if _, err := os.Stat(filepath.Join(runtimeRoot(t, repo), "stories", id, "plan.md")); err != nil {
		t.Fatalf("valid retry did not attach plan: %v", err)
	}
}

// criteriaSectionPlanSkill is planSkillFixture with the acceptance-criteria
// section contract switched on and a single attempt, so a malformed section
// fails the dispatch rather than being repaired.
var criteriaSectionPlanSkill = strings.Replace(planSkillFixture,
	"output_ac_coverage: true\n",
	"output_ac_coverage: true\noutput_criteria_section: Acceptance criteria\nattempt_max_total: 1\n", 1)

// criteriaPlanRoute is a backlog → plan(planner, propose) route whose entry edge
// carries a reviewer, so a refused plan has something to NOT reach. Neither
// writePlanDemoRoute (the reviewer sits on in_progress) nor spineFixture (cannot
// set propose) gives that shape.
func criteriaPlanRoute() (done, step string) {
	step = `[raised]
status = "backlog"
start = true

[ob-plan]
status = "plan"
agent = "planner"
skills = ["plan"]
propose = true
reviewers = ["satelle-story-plan-review"]
reviewer_agent = "reviewer"
requires = ["raised"]

[ob-done]
status = "done"
terminal = true
requires = ["ob-plan"]

[[gate]]
skill = "satelle-estimate-actual-review"
on = ["__never__"]
`
	done = "[\"*\"]\nobligations = [\"raised\", \"ob-plan\", \"ob-done\"]\n"
	return done, step
}

// plannerScriptFor returns a planner process that answers with body as the plan
// artifact.
func plannerScriptFor(t *testing.T, body string) string {
	t.Helper()
	env, err := json.Marshal(map[string]any{"artifact": map[string]string{"name": "plan", "type": "plan", "body": body}})
	if err != nil {
		t.Fatal(err)
	}
	return "#!/bin/sh\ncat >/dev/null\ncat <<'PLAN_JSON'\n" + string(env) + "\nPLAN_JSON\n"
}

// setupCriteriaDispatchRepo builds the criteria-section dispatch fixture: the
// propose+reviewer route, the contracted plan skill, and a reviewer that records
// every invocation to the returned path before accepting.
func setupCriteriaDispatchRepo(t *testing.T, planBody string) (repo, id, reviewerCalled string) {
	t.Helper()
	repo, id, _ = setupStructuredPlanRepo(t, plannerScriptFor(t, planBody))
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "plan.md"), criteriaSectionPlanSkill)

	// The route must really run the planner first and a reviewer after it; assert
	// that on the parsed route so a fixture that drifts fails instead of passing
	// vacuously.
	done, step := criteriaPlanRoute()
	doneDoc, stepDoc := routeFixture(done, step)
	spec, err := wfdot.ParseRoute(doneDoc, stepDoc, "plandemo", nil)
	if err != nil {
		t.Fatalf("derive criteria route: %v", err)
	}
	proposes := false
	for _, s := range spec.States {
		if s.Name == "plan" && s.Propose && s.Agent == "planner" {
			proposes = true
		}
	}
	reviewed := false
	for _, tr := range spec.Transitions {
		if tr.From == "backlog" && tr.To == "plan" {
			for _, sk := range tr.Skills {
				reviewed = reviewed || sk == "satelle-story-plan-review"
			}
		}
	}
	if !proposes || !reviewed {
		t.Fatalf("fixture route must propose via the planner and gate backlog→plan with a reviewer: proposes=%v reviewed=%v", proposes, reviewed)
	}
	writeRouteFixture(t, repo, done, step)

	reviewerCalled = filepath.Join(t.TempDir(), "reviewer-called")
	reviewer := filepath.Join(t.TempDir(), "recording-reviewer.sh")
	verdict := `{"decision":"accept","notes":""}`
	if err := os.WriteFile(reviewer, []byte(fmt.Sprintf(
		"#!/bin/sh\ncat >/dev/null\necho called >> %s\necho '%s'\n", reviewerCalled, verdict)), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	cur, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^command = "[^"\n]*claude-verdict-accept\.sh[^"\n]*"$`)
	if !re.Match(cur) {
		t.Fatalf("reviewer stub not found in agents.toml:\n%s", cur)
	}
	line := fmt.Sprintf("command = %q", reviewer+" {system} {tools} {model}")
	writeFile(t, agents, re.ReplaceAllLiteralString(string(cur), line))
	mustRun(t, testBin, repo, "reindex")
	return repo, id, reviewerCalled
}

// TestMalformedCriteriaSectionRefusedBeforeReview: a plan whose acceptance
// criteria section holds a line-number-prefixed line and a bullet is refused at
// backlog → plan, naming both lines, and no reviewer is ever dispatched.
func TestMalformedCriteriaSectionRefusedBeforeReview(t *testing.T) {
	body := "# Plan\n\n## Acceptance criteria\n1. the thing is done\n30|3. second\n- a bullet\n\n## AC1\nThe thing is covered."
	repo, id, reviewerCalled := setupCriteriaDispatchRepo(t, body)

	out, err := run(t, testBin, repo, "story", "set", id, "--status", "plan")
	if err == nil {
		t.Fatalf("malformed criteria section should refuse the transition:\n%s", out)
	}
	for _, want := range []string{"30|3. second", "- a bullet", "not a numbered criterion"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal does not name %q:\n%s", want, out)
		}
	}
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "backlog"`) {
		t.Errorf("refused plan advanced the story:\n%s", got)
	}
	if docs := mustRun(t, testBin, repo, "story", "docs", id); strings.Contains(docs, "plan") {
		t.Errorf("refused plan was attached:\n%s", docs)
	}
	if fileExists(reviewerCalled) {
		t.Error("a reviewer was dispatched for a plan with a malformed criteria section")
	}
}

// TestWellFormedCriteriaSectionReachesReview is the companion: a well-formed
// section passes, the plan attaches, and the reviewer on the same edge runs —
// proving the route above really has a live reviewer to be spared.
func TestWellFormedCriteriaSectionReachesReview(t *testing.T) {
	body := "# Plan\n\n## Acceptance criteria\n1. the thing is done\n\n## AC1\nThe thing is covered."
	repo, id, reviewerCalled := setupCriteriaDispatchRepo(t, body)

	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	if got := mustRun(t, testBin, repo, "story", "get", id); !strings.Contains(got, `"status": "plan"`) {
		t.Errorf("well-formed plan did not reach plan:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot(t, repo), "stories", id, "plan.md")); err != nil {
		t.Errorf("well-formed plan was not attached: %v", err)
	}
	if !fileExists(reviewerCalled) {
		t.Error("the reviewer on backlog → plan was never dispatched for a well-formed plan")
	}
}
