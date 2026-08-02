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
	materializeDefault(t, repo, "skills", "satelle-story-create-review")
	materializeDefault(t, repo, "workflows", "done")

	// Opt into create-gating via the local overlay (leaves the scaffold intact).
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")

	// Author the content rubric (the real one from this repo).
	rubric := substrateSkillBody(t, "satelle-story-create-review")
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "satelle-story-create-review.md"), rubric)

	// The create binding is DECLARED on the active workflow (sty_b031b29f), not a
	// hardcoded filename. This repo declares it on its DERIVED route's declaration
	// of done, so installing the route source is what wires content review
	// (sty_9835070d).
	seedRouteSource(t, repo)

	// Stub the reviewer harness to a deterministic verdict script.
	verdict := filepath.Join(repo, "verdict.sh")
	setVerdict := func(decision, notes string) {
		writeFile(t, verdict, fmt.Sprintf("#!/bin/sh\necho '{\"decision\":\"%s\",\"notes\":\"%s\"}'\n", decision, notes))
		_ = os.Chmod(verdict, 0o755)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\n", verdict))

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
	// Virtual baseline is not on disk; author our own wildcard only.

	// The create-review binding rides on done.md — a lifecycle hook is declared
	// where the declaration of done is (`satelle help workflow-convert`).
	done := `---
name: done
scope: project
type: workflow
tags: [type:workflow]
create_review: my-create-review
description: A declaration of done carrying the create-review binding under test.
---

## *
- raised
- coded
- closed
cancel: cancelled @satelle-story-cancel-review
`
	step := `---
name: step
scope: project
type: workflow
tags: [type:workflow]
description: The step catalogue for the create-review fixture route.
---

## backlog
start: true
provides: raised

## in_progress
agent: executor
provides: coded
requires: raised

## done
reviewers: satelle-story-done-review
reviewer_agent: reviewer
terminal: true
provides: closed
requires: coded
`
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), step)
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
	if out := mustRun(t, testBin, repo, "workflow", "validate"); !strings.Contains(out, "PASS  workflows/done") {
		t.Errorf("workflow validate should pass once the create_review skill resolves:\n%s", out)
	}
}

// TestCreateGateRejectsEpicAsFeature (sty_83782ffb AC4): a fresh init seeds
// gate_create=true, embeds satelle-story-create-review (via create_review on
// the baseline workflow), and a stubbed reviewer that rejects misclassified
// epics blocks creation — nothing persists.
func TestCreateGateRejectsEpicAsFeature(t *testing.T) {
	repo := t.TempDir()
	// Use run (not mustRun) so hermetic create-gate opt-out does not flip the
	// scaffold before we assert the product default.
	if out, err := run(t, testBin, repo, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// Confirm init seeded gate_create = true in the committed scaffold.
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "gate_create = true") {
		t.Fatalf("init must seed gate_create = true:\n%s", cfg)
	}
	// Virtual create_review skill + baseline: materialize for the stubbed gate path.
	materializeDefault(t, repo, "skills", "satelle-story-create-review")
	materializeDefault(t, repo, "workflows", "done")
	wf, err := os.ReadFile(filepath.Join(repo, ".satelle", "workflows", "done.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "create_review: satelle-story-create-review") {
		t.Fatalf("baseline must declare create_review:\n%s", wf)
	}

	// Stub the reviewer harness: reject when the draft looks like an epic
	// misfiled as feature (mirrors the classification rule in the rubric).
	verdict := filepath.Join(repo, "verdict.sh")
	writeFile(t, verdict, `#!/bin/sh
# Read stdin draft JSON; reject epic-as-feature, else accept.
IN=$(cat)
case "$IN" in
  *'"category":"feature"'*)
    case "$IN" in
      *epic:*|*'epic-parent'*|*'Umbrella'*|*'children'*)
        echo '{"decision":"reject","notes":"epic container must use category epic-parent, not feature"}'
        exit 0;;
    esac;;
esac
echo '{"decision":"accept","notes":""}'
`)
	_ = os.Chmod(verdict, 0o755)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[reviewer]\ncommand = \"%s {system} {tools} {model}\"\n", verdict))
	mustRun(t, testBin, repo, "reindex")

	// Draft epic with category feature — must be rejected.
	out, err := run(t, testBin, repo, "story", "create",
		"--category", "feature",
		"--title", "epic: channel alignment",
		"--body", "Umbrella for process-channel work; children close this epic.",
		"--acceptance", "1. every child is done or cancelled")
	if err == nil {
		t.Fatalf("epic-as-feature must be rejected; output:\n%s", out)
	}
	if !strings.Contains(out, "epic-parent") && !strings.Contains(out, "epic") {
		t.Errorf("reject notes should name classification fix:\n%s", out)
	}
	if list := mustRun(t, testBin, repo, "story", "list"); strings.Contains(list, "channel alignment") {
		t.Errorf("rejected draft must not persist:\n%s", list)
	}

	// Correct classification persists.
	if _, err := run(t, testBin, repo, "story", "create",
		"--category", "epic-parent",
		"--title", "epic: channel alignment",
		"--body", "Umbrella for process-channel work; children close this epic.",
		"--acceptance", "1. every child is done or cancelled"); err != nil {
		t.Fatalf("epic-parent create should accept: %v", err)
	}
}
