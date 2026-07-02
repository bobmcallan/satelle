//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateContentReviewGate drives the opt-in content/alignment create gate
// (sty_345e9ae7) end-to-end through the real binary: with create-gating on and
// the satelle-story-create-review rubric authored, a STRUCTURALLY-VALID draft is
// judged by the content reviewer. The reviewer harness is STUBBED to a
// deterministic verdict so the test is hermetic: a reject blocks creation (notes
// surfaced, nothing persisted), an accept persists. This proves the content gate
// is wired AFTER the structural check.
func TestCreateContentReviewGate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Opt into create-gating via the local overlay (leaves the scaffold intact).
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")

	// Author the content rubric (the real one from this repo).
	rubric, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "skills", "satelle-story-create-review.md"))
	if err != nil {
		t.Fatalf("read rubric source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "satelle-story-create-review.md"), string(rubric))

	// The create binding is DECLARED on the active workflow (sty_b031b29f), not a
	// hardcoded filename — install this repo's project workflow (which declares
	// create_review: satelle-story-create-review) so content review is wired.
	wf, err := os.ReadFile(filepath.Join(repoRootForTest(), ".satelle", "workflows", "satelle-project-workflow.md"))
	if err != nil {
		t.Fatalf("read workflow source: %v", err)
	}
	// Scope it to the "feature" category so it wins as a category-specific match
	// (independent of the embedded-baseline wildcard ordering).
	wfBody := strings.Replace(string(wf), `applies_to: ["*"]`, `applies_to: ["feature"]`, 1)
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), wfBody)

	// Stub the reviewer harness to a deterministic verdict script.
	verdict := filepath.Join(repo, "verdict.sh")
	setVerdict := func(decision, notes string) {
		writeFile(t, verdict, fmt.Sprintf("#!/bin/sh\necho '{\"decision\":\"%s\",\"notes\":\"%s\"}'\n", decision, notes))
		_ = os.Chmod(verdict, 0o755)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		fmt.Sprintf("[reviewer]\nharness = \"%s {system} {tools} {model}\"\n", verdict))

	setVerdict("reject", "stub: the ACs do not verify the goal")
	mustRun(t, testBin, repo, "reindex")

	// A structurally-valid draft is now blocked by the content reviewer's reject.
	out, err := run(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Add a widget", "--body", "Render a widget on the dashboard", "--acceptance", "1. the widget renders")
	if err == nil {
		t.Fatalf("content-review reject should block creation; output:\n%s", out)
	}
	if !strings.Contains(out, "the ACs do not verify the goal") {
		t.Errorf("reject notes not surfaced to the agent:\n%s", out)
	}
	if list := mustRun(t, testBin, repo, "story", "list"); strings.Contains(list, "Add a widget") {
		t.Errorf("a rejected draft must NOT persist:\n%s", list)
	}

	// Flip the verdict to accept: the same draft now persists.
	setVerdict("accept", "")
	if out, err := run(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Add a widget", "--body", "Render a widget on the dashboard", "--acceptance", "1. the widget renders"); err != nil {
		t.Fatalf("content-review accept should allow creation: %v\n%s", err, out)
	}
	if list := mustRun(t, testBin, repo, "story", "list"); !strings.Contains(list, "Add a widget") {
		t.Errorf("an accepted draft should persist:\n%s", list)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWorkflowValidateFlagsUnresolvedCreateReview proves the claim the
// create-review help topic makes (sty_51ad783b): `satelle workflow validate`
// flags a workflow that declares a create_review binding which does not resolve
// in the substrate — and passes once the rubric skill is authored (the topic's
// worked-example shape).
func TestWorkflowValidateFlagsUnresolvedCreateReview(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	wf := `---
name: my-project-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["*"]
create_review: my-create-review
description: A test workflow moving backlog → in_progress → done, carrying the create-review binding under test.
---

# workflow

` + "```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled [agent=reviewer]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
}` + "\n```\n"
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "my-project-workflow.md"), wf)
	mustRun(t, testBin, repo, "reindex")

	// Unresolved binding → workflow validate fails, naming it.
	out, err := run(t, testBin, repo, "workflow", "validate")
	if err == nil {
		t.Fatalf("workflow validate should fail on an unresolved create_review:\n%s", out)
	}
	if !strings.Contains(out, "create_review") || !strings.Contains(out, "my-create-review") {
		t.Errorf("the failure should name the unresolved create_review binding:\n%s", out)
	}

	// Author the rubric skill (the help topic's worked example) → clean pass.
	skill := `---
name: my-create-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Create gate — judges a story draft is aligned before it is persisted.
---

# Story create review

Judge the draft; reply with one JSON object:

` + "```json\n" + `{"decision": "accept", "notes": ""}` + "\n```\n"
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "my-create-review.md"), skill)
	mustRun(t, testBin, repo, "reindex")
	if out := mustRun(t, testBin, repo, "workflow", "validate"); !strings.Contains(out, "PASS  workflows/my-project-workflow") {
		t.Errorf("workflow validate should pass once the create_review skill resolves:\n%s", out)
	}
}
