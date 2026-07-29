//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return hookStdoutArgs(t, repo, []string{sub}, event)
}

// hookStdoutArgs runs `satelle hook <args…>` with event on stdin (sty_9e86f407).
func hookStdoutArgs(t *testing.T, repo string, args []string, event string) string {
	t.Helper()
	full := append([]string{"hook"}, args...)
	c := exec.Command(testBin, full...)
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

// TestHookCommitgateContainment (sty_aadd4d6c / sty_a8454d10): CLAUDE_PROJECT_DIR
// pins the anchor; a mutation into another git working tree is denied; a
// .git-less temp path is allowed; create cross-repo allows.
func TestHookCommitgateContainment(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Anchor must be a git tree so anchor-vs-foreign is meaningful.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Sibling repo (has .git) — foreign fence applies.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Non-repo temp — not fenced (pins the live defect).
	nonRepo := t.TempDir()

	// Foreign-tree mutation denied, reason names the path.
	ev := `{"tool_input":{"command":"rm ` + other + `/f.go"}}`
	c := exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(ev)
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("commitgate allowed foreign-tree rm; out=%s", out)
	}
	if !strings.Contains(string(out), other) {
		t.Errorf("deny must name the foreign path %q; out=%s", other, out)
	}

	// .git-less temp mutation is ALLOWED (sty_a8454d10 AC4).
	nonRepoEv := `{"tool_input":{"command":"rm ` + nonRepo + `/f.go"}}`
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)
	c.Stdin = strings.NewReader(nonRepoEv)
	if err := c.Run(); err != nil {
		t.Errorf("non-repo temp rm must be allowed by containment: %v", err)
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

	// Opt-in allows foreign-tree mutation.
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
		t.Errorf("allow_outside_tree_edits=true must permit foreign rm: %v", err)
	}
}

// TestHookCommitgateFdDuplication (sty_74c0556f): n>&m after a foreign cd must not
// be misread as a file redirect to cwd/&; real file redirects stay denied.
func TestHookCommitgateFdDuplication(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(isolatedEnv(t), "CLAUDE_PROJECT_DIR="+repo)

	// Allowed: cross-repo story verbs with fd-duplication (the live regression).
	allow := []string{
		"cd " + other + " && satelle story list 2>&1",
		"cd " + other + " && satelle story create --title x 2>&1",
		"cd " + other + " && satelle story list >&2",
		"cd " + other + " && satelle story list 1>&2",
		"cd " + other + " && satelle story list 2>&1 | head",
	}
	for _, cmd := range allow {
		c := exec.Command(testBin, "hook", "commitgate")
		c.Dir = repo
		c.Env = env
		c.Stdin = strings.NewReader(`{"tool_input":{"command":` + strconv.Quote(cmd) + `}}`)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Errorf("want allow for %q; err=%v out=%s", cmd, err, out)
		}
		if strings.Contains(string(out), filepath.Join(other, "&")) || strings.Contains(string(out), "refusing Bash mutation") {
			t.Errorf("fd-dup must not produce fence deny for %q; out=%s", cmd, out)
		}
	}

	// Denied: real file redirects into the foreign tree (policy unchanged).
	deny := []string{
		"cd " + other + " && echo x > f.txt",
		"cd " + other + " && echo x 2> err.log",
		"cd " + other + " && echo x &> out.log",
		"cd " + other + " && echo x >& out.log",
	}
	for _, cmd := range deny {
		c := exec.Command(testBin, "hook", "commitgate")
		c.Dir = repo
		c.Env = env
		c.Stdin = strings.NewReader(`{"tool_input":{"command":` + strconv.Quote(cmd) + `}}`)
		out, err := c.CombinedOutput()
		if err == nil {
			t.Errorf("want deny for file redirect %q; out=%s", cmd, out)
		}
		if strings.Contains(string(out), filepath.Join(other, "&")) {
			t.Errorf("deny for %q must not name bogus path …/&; out=%s", cmd, out)
		}
	}
}

// TestHookPromptReminderAndLivenessWarning: UserPromptSubmit always re-injects the
// engaged-story reminder, and the gate-liveness self-check adds a LOUD warning
// only when the edit gate is confidently NOT wired (the inert-gate countermeasure).
func TestHookPromptReminderAndLivenessWarning(t *testing.T) {
	repo := t.TempDir()
	// Explicit harness: bare init no longer scaffolds claude by default
	// (epic:minimal-harness-footprint).
	mustRun(t, testBin, repo, "init", "--harness", "claude")

	out := mustRun(t, testBin, repo, "hook", "prompt")
	if !strings.Contains(out, "edits require an ENGAGED story") {
		t.Errorf("prompt did not re-inject the engaged-story reminder:\n%s", out)
	}
	if strings.Contains(out, "NOT wired") {
		t.Errorf("false liveness warning while the gate IS wired:\n%s", out)
	}

	// Strip the edit gate → the self-check must warn LOUDLY.
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
// sty_9e86f407: --harness codex forces Claude envelope on no-story deny.
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

	// Explicit --harness codex (wrapper forwards this after agents install).
	codexOut := hookStdoutArgs(t, repo, []string{"gate", "--harness", "codex"},
		`{"tool_input":{"file_path":"`+code+`"}}`)
	if !strings.Contains(codexOut, `"permissionDecision":"deny"`) {
		t.Errorf("Codex deny missing permissionDecision:\n%s", codexOut)
	}
	if strings.Contains(codexOut, `"decision":"deny"`) && !strings.Contains(codexOut, "hookSpecificOutput") {
		t.Errorf("Codex deny must use Claude envelope:\n%s", codexOut)
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

// commandAllowWorkflow is gate-free with a release step so step policy can be
// exercised without agent CLIs (sty_c21490cc).
const commandAllowWorkflow = `---
name: wf-cmd-allow
type: workflow
description: gate-free lifecycle with release for command_allow tests
applies_to: ["feature"]
scope: project
---

` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  plan [agent=executor]
  in_progress [agent=executor]
  release [agent=executor]
  done [shape=Msquare]
  backlog -> plan -> in_progress -> release -> done
}
` + "```\n"

// TestHookCommitgateCommandAllow (sty_c21490cc): opt-in [gate.command_allow]
// blocks git push at in_progress, allows it at release; unconfigured engage-only
// path is unchanged.
func TestHookCommitgateCommandAllow(t *testing.T) {
	pushEv := `{"tool_input":{"command":"git push origin main"}}`

	// --- unconfigured: engaged at in_progress → push allowed (engage-only) ---
	repoOpen := t.TempDir()
	mustRun(t, testBin, repoOpen, "init")
	writeFile(t, filepath.Join(repoOpen, ".satelle", "workflows", "wf-cmd-allow.md"), commandAllowWorkflow)
	mustRun(t, testBin, repoOpen, "reindex", "--validate=false")
	out := mustRun(t, testBin, repoOpen, "story", "create", "--category", "feature",
		"--title", "t", "--body", "b", "--acceptance", "1. a")
	idOpen := extractID(out, "sty_")
	mustRun(t, testBin, repoOpen, "story", "set", idOpen, "--status", "plan")
	mustRun(t, testBin, repoOpen, "story", "set", idOpen, "--status", "in_progress")
	c := exec.Command(testBin, "hook", "commitgate")
	c.Dir = repoOpen
	c.Env = isolatedEnv(t)
	c.Stdin = strings.NewReader(pushEv)
	if err := c.Run(); err != nil {
		t.Fatalf("unconfigured + engaged in_progress: push must allow (engage-only): %v", err)
	}

	// --- configured: push only at release ---
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "wf-cmd-allow.md"), commandAllowWorkflow)
	// Append command_allow to satelle.toml
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("\n[gate.command_allow]\npush = [\"release\"]\n")...)
	if err := os.WriteFile(tomlPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")
	out = mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "t", "--body", "b", "--acceptance", "1. a")
	id := extractID(out, "sty_")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")
	mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")

	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = isolatedEnv(t)
	c.Stdin = strings.NewReader(pushEv)
	outB, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("configured + in_progress: push must deny; out=%s", outB)
	}
	if !strings.Contains(string(outB), "release") {
		t.Errorf("deny reason must name allowed state release; out=%s", outB)
	}

	mustRun(t, testBin, repo, "story", "set", id, "--status", "release")
	c = exec.Command(testBin, "hook", "commitgate")
	c.Dir = repo
	c.Env = isolatedEnv(t)
	c.Stdin = strings.NewReader(pushEv)
	if err := c.Run(); err != nil {
		t.Fatalf("configured + release: push must allow: %v", err)
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
