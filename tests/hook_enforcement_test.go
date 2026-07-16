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
	c.Env = isolatedEnv(t)
	c.Stdin = strings.NewReader(event)
	out, _ := c.Output()
	return string(out)
}

// TestInitSeedsAndInjectsEditsPrinciple proves the embedded operating principle
// is SEEDED by init (AC1) and INJECTED at SessionStart (AC2).
func TestInitSeedsAndInjectsEditsPrinciple(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Virtual principle: resolve via List overlay; inject without on-disk seed.
	mustRun(t, testBin, repo, "reindex", "--validate=false")
	ctx := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(ctx, "Edits require a story") {
		t.Errorf("SessionStart did not inject the edits-require-a-story principle:\n%s", ctx)
	}
	// sty_aadd4d6c AC6: cross-repo containment principle is session-injected.
	if !strings.Contains(ctx, "Cross-repo containment") && !strings.Contains(ctx, "Create anywhere") {
		t.Errorf("SessionStart did not inject cross-repo containment:\n%s", ctx)
	}
}

// TestHookCommitgateContainment (sty_aadd4d6c): CLAUDE_PROJECT_DIR pins the
// anchor; a cd-elsewhere mutation is denied; the same form inside home only
// reaches the engaged-story rule (not containment); create cross-repo allows.
func TestHookCommitgateContainment(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	other := t.TempDir()

	// Outside-tree mutation denied, reason names the path.
	ev := `{"tool_input":{"command":"rm ` + other + `/f.go"}}`
	c := exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(ev)
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("commitgate allowed outside-tree rm; out=%s", out)
	}
	if !strings.Contains(string(out), other) {
		t.Errorf("deny must name the outside path %q; out=%s", other, out)
	}

	// story create (no outside mutation path) allows through containment.
	createEv := `{"tool_input":{"command":"satelle story create --title t --body b --acceptance '1. a'"}}`
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(createEv)
	if err := c.Run(); err != nil {
		t.Errorf("story create must be allowed by containment: %v", err)
	}

	// In-home non-commit command allows (no engaged-story check for non-commit).
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(`{"tool_input":{"command":"ls internal"}}`)
	if err := c.Run(); err != nil {
		t.Errorf("in-home ls must allow: %v", err)
	}

	// In-home git commit still hits engaged-story rule (no story → deny).
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(`{"tool_input":{"command":"git commit -m x"}}`)
	if err := c.Run(); err == nil {
		t.Error("in-home git commit with no engaged story must deny")
	}

	// Opt-in allows outside-tree mutation.
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	raw, rerr := os.ReadFile(tomlPath)
	if rerr != nil {
		// Fall back to repo-root satelle.toml if present.
		tomlPath = filepath.Join(repo, "satelle.toml")
		raw, rerr = os.ReadFile(tomlPath)
		if rerr != nil {
			t.Fatal(rerr)
		}
	}
	var patched string
	if idx := strings.Index(string(raw), "[gate]\n"); idx >= 0 {
		at := idx + len("[gate]\n")
		patched = string(raw[:at]) + "allow_outside_tree_edits = true\n" + string(raw[at:])
	} else {
		patched = string(raw) + "\n[gate]\nallow_outside_tree_edits = true\n"
	}
	if err := os.WriteFile(tomlPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(ev)
	if err := c.Run(); err != nil {
		t.Errorf("allow_outside_tree_edits=true must permit outside rm: %v", err)
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

// TestHookGateHarnessSpecificDenyShape (sty_5e4bc568): a denied edit emits ONLY
// the Claude shape for tool_input envelopes and ONLY the Grok shape for toolInput.
// Dual-format was the inert-gate bug (Claude schema rejects top-level decision).
func TestHookGateHarnessSpecificDenyShape(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	code := filepath.Join(repo, "internal", "x.go")

	claudeOut := hookStdout(t, repo, "gate",
		`{"tool_input":{"file_path":"`+code+`"}}`)
	if !strings.Contains(claudeOut, `"permissionDecision":"deny"`) {
		t.Errorf("Claude deny missing permissionDecision:\n%s", claudeOut)
	}
	if strings.Contains(claudeOut, `"decision":"deny"`) {
		t.Errorf("Claude deny must not carry top-level decision:\n%s", claudeOut)
	}

	grokOut := hookStdout(t, repo, "gate",
		`{"toolInput":{"path":"`+code+`"}}`)
	if !strings.Contains(grokOut, `"decision":"deny"`) {
		t.Errorf("Grok deny missing top-level decision:\n%s", grokOut)
	}
	if strings.Contains(grokOut, `"hookSpecificOutput"`) {
		t.Errorf("Grok deny must not carry hookSpecificOutput:\n%s", grokOut)
	}
}

// TestHookCommitgateHarnessDenyShape: commitgate shares denyPreToolUse — snake
// Bash events get Claude shape, camelCase get Grok (sty_5e4bc568 AC3).
func TestHookCommitgateHarnessDenyShape(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	claudeOut := hookStdout(t, repo, "commitgate",
		`{"tool_input":{"command":"git commit -m x"}}`)
	if !strings.Contains(claudeOut, `"permissionDecision":"deny"`) {
		t.Errorf("Claude commitgate deny missing permissionDecision:\n%s", claudeOut)
	}
	if strings.Contains(claudeOut, `"decision":"deny"`) {
		t.Errorf("Claude commitgate deny must not carry top-level decision:\n%s", claudeOut)
	}

	grokOut := hookStdout(t, repo, "commitgate",
		`{"toolInput":{"command":"git commit -m x"}}`)
	if !strings.Contains(grokOut, `"decision":"deny"`) {
		t.Errorf("Grok commitgate deny missing top-level decision:\n%s", grokOut)
	}
	if strings.Contains(grokOut, `"hookSpecificOutput"`) {
		t.Errorf("Grok commitgate deny must not carry hookSpecificOutput:\n%s", grokOut)
	}
}

// TestHookCommitgateDeniesWithoutStory: the Bash commitgate blocks a git commit
// when no story is engaged (AC7 commitgate analogue).
func TestHookCommitgateDeniesWithoutStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	c := exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = isolatedEnv(t)
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
