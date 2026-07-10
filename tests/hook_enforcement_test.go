//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enforcementWorkflow is a minimal GATE-FREE workflow (no reviewer edges) so a
// feature story can be engaged without an agent CLI — the same technique
// coder_dispatch_test uses. plan and in_progress are non-terminal engaging states.
const enforcementWorkflow = `---
name: wf-enforce
type: workflow
description: gate-free lifecycle for exercising the edit gate under an engaged story
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan [agent=executor]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> done
}
` + "```\n"

// gitBaseline makes repo a git repo with a clean tree (everything committed), so
// a later `git status --porcelain` reflects only the test's intentional changes —
// dirtyGatedPaths is what the Stop hook inspects.
func gitBaseline(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "base"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// hookStdout runs `satelle hook <sub>` in repo with event on stdin and returns its
// stdout (ignoring a non-zero exit — a deny/block still writes the payload).
func hookStdout(t *testing.T, repo, sub, event string) string {
	t.Helper()
	c := exec.Command(testBin, "hook", sub)
	c.Dir = repo
	c.Stdin = strings.NewReader(event)
	out, _ := c.Output()
	return string(out)
}

// TestInitSeedsAndInjectsEditsPrinciple proves the embedded operating principle
// is SEEDED by init (AC1) and INJECTED at SessionStart (AC2).
func TestInitSeedsAndInjectsEditsPrinciple(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "principles", "satelle-edits-require-a-story.md")); err != nil {
		t.Fatalf("init did not seed the edits-require-a-story principle: %v", err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")
	ctx := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(ctx, "Edits require a story") {
		t.Errorf("SessionStart did not inject the edits-require-a-story principle:\n%s", ctx)
	}
}

// TestHookPromptReminderAndLivenessWarning: UserPromptSubmit always re-injects the
// engaged-story reminder, and the gate-liveness self-check adds a LOUD warning
// only when the edit gate is confidently NOT wired (the inert-gate countermeasure).
func TestHookPromptReminderAndLivenessWarning(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init") // seeds .claude/settings.json with the gate wired

	out := mustRun(t, testBin, repo, "hook", "prompt")
	if !strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("prompt did not re-inject the engaged-story reminder:\n%s", out)
	}
	if strings.Contains(out, "NOT wired") {
		t.Errorf("false liveness warning while the gate IS wired:\n%s", out)
	}

	// Strip the edit gate from EVERY candidate settings file (init may have
	// scaffolded both .claude and .grok) → the self-check must warn LOUDLY.
	writeFile(t, filepath.Join(repo, ".claude", "settings.json"),
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"satelle reindex"}]}]}}`)
	_ = os.RemoveAll(filepath.Join(repo, ".grok"))
	out = mustRun(t, testBin, repo, "hook", "prompt")
	if !strings.Contains(out, "NOT wired") {
		t.Errorf("expected the LOUD not-wired warning once the gate is removed:\n%s", out)
	}
}

// TestHookGateAllowsUnderEngagedStory proves the positive case: a code edit is
// BLOCKED with no engaged story and ALLOWED once a story is engaged.
func TestHookGateAllowsUnderEngagedStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-enforce.md"), enforcementWorkflow)
	mustRun(t, testBin, repo, "reindex")

	code := filepath.Join(repo, "internal", "x.go")
	if gateEvent(t, repo, code) {
		t.Error("gate ALLOWED a code edit with no engaged story")
	}

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "x", "--body", "b", "--acceptance", "1. a")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")

	if !gateEvent(t, repo, code) {
		t.Error("gate BLOCKED a code edit while a story is engaged")
	}
}

// TestHookGateDualHarnessDenyShape: a denied edit carries BOTH the Claude
// (hookSpecificOutput.permissionDecision=deny) and Grok (top-level decision=deny)
// fields, so either harness surfaces the reason (AC6 cross-harness).
func TestHookGateDualHarnessDenyShape(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	out := hookStdout(t, repo, "gate",
		`{"tool_input":{"file_path":"`+filepath.Join(repo, "internal", "x.go")+`"}}`)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("deny missing Claude permissionDecision field:\n%s", out)
	}
	if !strings.Contains(out, `"decision":"deny"`) {
		t.Errorf("deny missing Grok top-level decision field:\n%s", out)
	}
}

// TestHookCommitgateDeniesWithoutStory: the Bash commitgate blocks a git commit
// when no story is engaged (AC7 commitgate analogue).
func TestHookCommitgateDeniesWithoutStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	c := exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Stdin = strings.NewReader(`{"tool_input":{"command":"git commit -m x"}}`)
	if err := c.Run(); err == nil {
		t.Error("commitgate ALLOWED a git commit with no engaged story")
	}
}

// TestHookStopcheck: the Stop post-hoc detector blocks a finish when the tree has
// ungated non-exempt changes and no story is engaged, honours stop_hook_active,
// and stays quiet for exempt-only changes or when a story is engaged.
func TestHookStopcheck(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-enforce.md"), enforcementWorkflow)
	mustRun(t, testBin, repo, "reindex")
	gitBaseline(t, repo) // clean tree

	// A non-exempt code edit with no engaged story → BLOCK naming the file.
	// (repo root is non-exempt; only .satelle/ is seeded exempt.)
	writeFile(t, filepath.Join(repo, "ungated.go"), "package x\n")
	out := hookStdout(t, repo, "stopcheck", `{}`)
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "ungated.go") {
		t.Errorf("stopcheck should block an ungated edit naming the file:\n%s", out)
	}

	// Anti-loop: the SAME dirty state but stop_hook_active=true → never re-block.
	if out := hookStdout(t, repo, "stopcheck", `{"stop_hook_active":true}`); strings.Contains(out, "block") {
		t.Errorf("stopcheck must honour stop_hook_active (no re-block):\n%s", out)
	}

	// Engage a story → the same dirty edit is now legitimate → quiet (no block).
	sid := extractID(mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "x", "--body", "b", "--acceptance", "1. a"), "sty_")
	mustRun(t, testBin, repo, "story", "set", sid, "--status", "plan")
	if out := hookStdout(t, repo, "stopcheck", `{}`); strings.Contains(out, "block") {
		t.Errorf("stopcheck must stay quiet while a story is engaged:\n%s", out)
	}
}
