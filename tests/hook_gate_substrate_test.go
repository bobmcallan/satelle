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

// gateEventRaw runs `satelle hook gate` with a verbatim PreToolUse event JSON on
// stdin (so a test can send a Grok-shaped envelope or a relative path), returning
// whether it ALLOWED the edit (exit 0).
func gateEventRaw(t *testing.T, repo, eventJSON string) bool {
	t.Helper()
	cmd := exec.Command(testBin, "hook", "gate")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(eventJSON)
	err := cmd.Run()
	return err == nil
}

// setExemptPaths overwrites repo's satelle.toml [gate] edit_exempt_paths with the
// given prefixes (empty for none), replacing whatever init seeded. A minimal
// satelle.toml is valid config, so this is the clean way to pin the exempt list a
// case needs without appending a duplicate [gate] table.
func setExemptPaths(t *testing.T, repo string, prefixes ...string) {
	t.Helper()
	quoted := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		quoted = append(quoted, `"`+p+`"`)
	}
	body := "[gate]\nedit_exempt_paths = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write satelle.toml: %v", err)
	}
}

// TestHookGateExemptsSubstrate drives the real binary to prove the edit gate
// exempts authored substrate under .satelle/ while still gating in-repo code, with
// NO story engaged. Exemption is now config, not code (sty_8c3d345c): init SEEDS
// edit_exempt_paths = [".satelle/"], so a fresh repo keeps substrate editable via
// that seeded config — the binary no longer special-cases the data dir.
func TestHookGateExemptsSubstrate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// No story engaged. Authored substrate under .satelle/ is ALLOWED (seeded exempt) …
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
	// … and an out-of-repo path is REFUSED (cross-repo lock, sty_3026d890) —
	// including former "session scratch" under /tmp and sibling trees.
	if gateEvent(t, repo, "/tmp/claude/scratch/foo.sh") {
		t.Error("edit gate allowed an out-of-repo path (must refuse cross-repo edits)")
	}
	sibling := filepath.Join(filepath.Dir(repo), "other-project", "main.go")
	if gateEvent(t, repo, sibling) {
		t.Error("edit gate allowed a sibling-tree path (must refuse cross-repo edits)")
	}
}

// TestHookGateExemptsConfiguredPaths drives the real binary to prove exemption is
// CONFIG-only (sty_8c3d345c): a harness authoring dir (.claude/) is CODE — blocked —
// until a repo lists it in [gate] edit_exempt_paths, after which its edits are exempt
// while in-repo code stays gated, all with NO story engaged. This is AC3's before/after.
func TestHookGateExemptsConfiguredPaths(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	claudeSkill := filepath.Join(repo, ".claude", "skills", "audit", "SKILL.md")
	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")

	// BEFORE: init seeds only .satelle/, so a .claude/ edit is in-repo code — blocked
	// (no story engaged), the binary staying CLI-vendor-neutral by default.
	if gateEvent(t, repo, claudeSkill) {
		t.Error("edit gate allowed a .claude/ edit before it was configured exempt (should be blocked as code)")
	}

	// A repo opts .claude/ in (keeping .satelle/) via satelle.toml config.
	setExemptPaths(t, repo, ".satelle/", ".claude/")

	// AFTER: the .claude/ edit is now exempt (configured, not code) …
	if !gateEvent(t, repo, claudeSkill) {
		t.Error("edit gate blocked a .claude/ edit after [gate] edit_exempt_paths opted it in (should be exempt)")
	}
	// … the configured data dir still works …
	if !gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Error("edit gate blocked authored substrate under a configured .satelle/ prefix (should be exempt)")
	}
	// … and in-repo CODE outside the exempt prefixes stays BLOCKED.
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed an in-repo code edit with no engaged story")
	}
}

// TestHookGateRelativePathGrokDenied is the AC1 regression (sty_8c3d345c): a
// repo-RELATIVE product-code target — the shape Grok sends, and the exact input
// that previously nested under the data dir and bypassed the gate — is DENIED with
// no engaged story, identically to the absolute path. Both the snake_case relative
// file_path and a Grok camelCase envelope are exercised.
func TestHookGateRelativePathGrokDenied(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Absolute (Claude) — denied.
	if gateEvent(t, repo, filepath.Join(repo, "internal", "config", "sync.go")) {
		t.Error("edit gate allowed an ABSOLUTE in-repo code edit with no engaged story")
	}
	// Relative file_path — must be DENIED too (the pre-fix bypass).
	if gateEventRaw(t, repo, `{"tool_input":{"file_path":"internal/config/sync.go"}}`) {
		t.Error("edit gate allowed a RELATIVE code path (the pre-fix relative-path bypass)")
	}
	// Grok camelCase envelope with a relative path — also DENIED.
	if gateEventRaw(t, repo, `{"toolInput":{"path":"internal/cli/cmd_hook.go"}}`) {
		t.Error("edit gate allowed a Grok-shape relative code path (must be gated like any code)")
	}
	// A relative SUBSTRATE path under the seeded .satelle/ prefix stays exempt.
	if !gateEventRaw(t, repo, `{"tool_input":{"file_path":".satelle/skills/plan.md"}}`) {
		t.Error("edit gate blocked a relative .satelle/ substrate path (seeded exempt — should be allowed)")
	}
}

// TestHookGateConfigDecidesSubstrate is the AC2 regression (sty_8c3d345c): the
// binary does NOT hardcode the data dir. With an explicitly EMPTY edit_exempt_paths,
// even a .satelle/ substrate edit is DENIED with no engaged story — config decides,
// not a Go rule.
func TestHookGateConfigDecidesSubstrate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Seeded default (.satelle/ exempt): the substrate edit is allowed.
	if !gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Fatal("edit gate blocked a .satelle/ edit under the seeded exempt list (should be allowed)")
	}
	// Empty the list: now the SAME edit is DENIED — no hardcoded data-dir exemption.
	setExemptPaths(t, repo)
	if gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Error("edit gate allowed a .satelle/ edit with an empty edit_exempt_paths (data dir must not be hardcoded exempt)")
	}
}
