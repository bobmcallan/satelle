//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// `plan` is authored here; satelle-story-plan-review is an embedded default
	// served by the overlay. substrateSkillBody resolves each where it lives.
	for _, name := range []string{"plan", "satelle-story-plan-review"} {
		writeFile(t, filepath.Join(repo, ".satelle", "skills", name+".md"), substrateSkillBody(t, name))
	}
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
