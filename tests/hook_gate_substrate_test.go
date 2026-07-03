//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gateEvent runs `satelle hook gate` in repo with a PreToolUse edit event for the
// given file path on stdin, returning whether it ALLOWED the edit (exit 0).
func gateEvent(t *testing.T, repo, filePath string) bool {
	t.Helper()
	cmd := exec.Command(testBin, "hook", "gate")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(`{"tool_input":{"file_path":"` + filePath + `"}}`)
	err := cmd.Run()
	return err == nil
}

// TestHookGateExemptsSubstrate drives the real binary to prove the edit gate
// exempts authored substrate under the data dir while still gating in-repo code,
// with NO story engaged (sty_103af456).
func TestHookGateExemptsSubstrate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// No story engaged. Authored substrate under .satelle/ is ALLOWED …
	for _, sub := range []string{
		filepath.Join(repo, ".satelle", "skills", "plan.md"),
		filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"),
		filepath.Join(repo, ".satelle", "agents.toml"),
	} {
		if !gateEvent(t, repo, sub) {
			t.Errorf("edit gate blocked authored substrate %s (should be exempt)", sub)
		}
	}
	// … in-repo CODE is still BLOCKED (needs an engaged story) …
	if gateEvent(t, repo, filepath.Join(repo, "internal", "cli", "cmd_hook.go")) {
		t.Error("edit gate allowed an in-repo code edit with no engaged story")
	}
	// … and an out-of-repo scratch path stays exempt.
	if !gateEvent(t, repo, "/tmp/claude/scratch/foo.sh") {
		t.Error("edit gate blocked an out-of-repo scratch edit (should be exempt)")
	}
}

// TestHookGateExemptsConfiguredPaths drives the real binary to prove the
// config-driven [gate] edit_exempt_paths generalizes the data-dir exemption
// (sty_41416b76): a harness authoring dir (.claude/) is CODE — blocked — until a
// repo opts it in via satelle.toml, after which its edits are exempt while in-repo
// code stays gated, all with NO story engaged. This is AC3's before/after.
func TestHookGateExemptsConfiguredPaths(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	claudeSkill := filepath.Join(repo, ".claude", "skills", "audit", "SKILL.md")
	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")

	// BEFORE: with no [gate] config, a .claude/ edit is in-repo code — blocked
	// (no story engaged), the binary staying CLI-vendor-neutral by default.
	if gateEvent(t, repo, claudeSkill) {
		t.Error("edit gate allowed a .claude/ edit before it was configured exempt (should be blocked as code)")
	}

	// A repo opts .claude/ in via satelle.toml — itself an exempt substrate edit.
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	f, err := os.OpenFile(tomlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open satelle.toml: %v", err)
	}
	if _, err := f.WriteString("\n[gate]\nedit_exempt_paths = [\".claude/\"]\n"); err != nil {
		t.Fatalf("append [gate]: %v", err)
	}
	_ = f.Close()

	// AFTER: the .claude/ edit is now exempt (authored, not code) …
	if !gateEvent(t, repo, claudeSkill) {
		t.Error("edit gate blocked a .claude/ edit after [gate] edit_exempt_paths opted it in (should be exempt)")
	}
	// … the always-exempt data dir still works …
	if !gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Error("edit gate blocked authored substrate under the data dir (should be exempt)")
	}
	// … and in-repo CODE outside the exempt prefixes stays BLOCKED.
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed an in-repo code edit with no engaged story")
	}
}
