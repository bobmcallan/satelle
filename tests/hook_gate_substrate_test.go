//go:build integration

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// substrateWarnMarker is the stable substring of the no-engaged-story nudge the
// integration tests look for (the full message lives in package cli). It captures
// the nudge's meaning without coupling to the exact wording.
const substrateWarnMarker = "no engaged story"

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

// gateEventCapture runs `satelle hook gate` for filePath, returning whether it
// ALLOWED the edit (exit 0) plus captured stdout (the PreToolUse JSON whose
// additionalContext is the model-visible nudge) and stderr (the human mirror).
func gateEventCapture(t *testing.T, repo, filePath string) (allowed bool, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(testBin, "hook", "gate")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(`{"tool_input":{"file_path":"` + filePath + `"}}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return err == nil, out.String(), errb.String()
}

// TestHookGateWarnsSubstrateWithoutEngagedStory (sty_f5f351d1 AC1): a data-dir
// substrate edit with NO engaged story is ALLOWED (exit 0) and emits the nudge —
// the warning rides the PreToolUse additionalContext JSON on stdout (the only
// model-visible channel on an allowed edit; bare stderr is transcript-only on
// exit 0), mirrored to stderr for the human watching the session.
func TestHookGateWarnsSubstrateWithoutEngagedStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	sub := filepath.Join(repo, ".satelle", "skills", "plan.md")
	allowed, stdout, stderr := gateEventCapture(t, repo, sub)
	if !allowed {
		t.Fatal("edit gate blocked a data-dir substrate edit (should be fail-open allowed)")
	}
	if !strings.Contains(stdout, substrateWarnMarker) {
		t.Errorf("want the nudge in stdout additionalContext (model-visible); got stdout=%q", stdout)
	}
	if !strings.Contains(stderr, substrateWarnMarker) {
		t.Errorf("want the nudge mirrored to stderr (human transcript); got stderr=%q", stderr)
	}
}

// TestHookGateSilentSubstrateWithEngagedStory (sty_f5f351d1 AC2): the same data-dir
// edit is silently allowed (NO nudge) once a substrate story is engaged — in_progress
// is the substrate workflow's performing state, so storyEngaged() is true.
func TestHookGateSilentSubstrateWithEngagedStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Engage a substrate story (substrate workflow has no estimate gate, so
	// in_progress is reachable without ceremony).
	mustRun(t, testBin, repo, "story", "create", "--category", "substrate",
		"--title", "track this", "--acceptance", "1. land a substrate slice", "--status", "in_progress")

	sub := filepath.Join(repo, ".satelle", "skills", "plan.md")
	allowed, stdout, stderr := gateEventCapture(t, repo, sub)
	if !allowed {
		t.Fatal("edit gate blocked a data-dir substrate edit (should be allowed)")
	}
	if strings.Contains(stdout, substrateWarnMarker) || strings.Contains(stderr, substrateWarnMarker) {
		t.Errorf("edit gate nudged on a substrate edit WITH a story engaged (should be silent); stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestHookGateSilentOnExemptPathEdit (sty_f5f351d1 AC1 scope): an edit to a
// configured [gate] edit_exempt_paths dir (.claude/) with no story engaged is
// allowed but NOT nudged — the warning is scoped to the data dir only, so a repo's
// deliberate opt-in dirs stay quiet.
func TestHookGateSilentOnExemptPathEdit(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	f, err := os.OpenFile(tomlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open satelle.toml: %v", err)
	}
	if _, err := f.WriteString("\n[gate]\nedit_exempt_paths = [\".claude/\"]\n"); err != nil {
		t.Fatalf("append [gate]: %v", err)
	}
	_ = f.Close()

	claudeSkill := filepath.Join(repo, ".claude", "skills", "audit", "SKILL.md")
	allowed, stdout, stderr := gateEventCapture(t, repo, claudeSkill)
	if !allowed {
		t.Fatal("edit gate blocked an edit_exempt_paths edit (should be allowed)")
	}
	if strings.Contains(stdout, substrateWarnMarker) || strings.Contains(stderr, substrateWarnMarker) {
		t.Errorf("edit gate nudged on an edit_exempt_paths edit (should be silent — data-dir scope only); stdout=%q stderr=%q", stdout, stderr)
	}
}
