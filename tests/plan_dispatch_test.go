//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planDemoWorkflow isolates the dispatched plan step: backlog → plan(planner) →
// in_progress(in-loop) → done, with the plan → in_progress edge gated by the
// real satelle-story-plan-review.
const planDemoWorkflow = `---
name: plan-demo-workflow
type: workflow
description: isolates the dispatched fable plan step and the plan-review gate
applies_to: ["plandemo"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan [agent=planner, prompt="@skill:plan"]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> plan
  plan -> in_progress [reviewer_skill="satelle-story-plan-review"]
  in_progress -> done
}
` + "```\n"

// TestPlanStepDispatchesFableAndCapturesArtifact drives the real binary to prove
// the plan step (sty_d9a0b573): entering `plan` dispatches the named planner
// binding, whose run captures an implementation plan AS A STORY ARTIFACT under
// .satelle/stories/<id>/, and the plan-review gate then admits it to in_progress.
func TestPlanStepDispatchesFableAndCapturesArtifact(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// plandemo is a fixture-only category — silence the default-warn notice.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	stubReviewerAccept(t, repo) // the plan-review gate accepts via the stub reviewer

	// Copy the real plan executor + plan-review gate skills into the repo so the
	// isolated workflow's references resolve.
	for _, name := range []string{"plan", "satelle-story-plan-review"} {
		body, err := os.ReadFile(filepath.Join("..", ".satelle", "skills", name+".md"))
		if err != nil {
			t.Fatalf("read skill %s: %v", name, err)
		}
		writeFile(t, filepath.Join(repo, ".satelle", "skills", name+".md"), string(body))
	}

	// A fake planner harness: it reads the payload on stdin and attaches a plan to
	// the story (standing in for the fable model), then exits 0.
	script := filepath.Join(repo, "planner.sh")
	body := "#!/bin/sh\np=$(cat)\nid=$(printf '%s' \"$p\" | grep -oE 'sty_[0-9a-f]+' | head -1)\n\"" +
		testBin + "\" story attach \"$id\" --name plan --type plan --body \"PLAN: covers every acceptance criterion\"\necho planned\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// stubReviewerAccept rewrote agents.toml to just [reviewer]; append [planner].
	f, err := os.OpenFile(filepath.Join(repo, ".satelle", "agents.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(fmt.Sprintf("\n[planner]\ncommand = \"%s {system}\"\ntools = \"Read,Bash(satelle:*)\"\nmodel = \"fable\"\n", script)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "plan-demo-workflow.md"), planDemoWorkflow)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "plandemo",
		"--title", "Plan me", "--body", "do the thing", "--acceptance", "1. the thing is done")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id:\n%s", out)
	}

	// Enter plan → the planner is dispatched and captures the plan artifact.
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	planDoc := filepath.Join(runtimeRoot(t, repo), "stories", id, "plan.md")
	if _, err := os.Stat(planDoc); err != nil {
		t.Fatalf("plan step did not capture a plan artifact under the story: %v", err)
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
