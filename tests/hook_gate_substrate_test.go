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
	cmd.Env = isolatedEnv(t)
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
	cmd.Env = isolatedEnv(t)
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

// unlockSubstrate appends the documented opt-out, an empty [gate]
// lock_substrate_paths, to the [gate] table setExemptPaths wrote, so a case can
// see what edit_exempt_paths alone decides once no prefix is locked.
func unlockSubstrate(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, ".satelle", "satelle.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read satelle.toml: %v", err)
	}
	if err := os.WriteFile(path, append(body, []byte("lock_substrate_paths = []\n")...), 0o644); err != nil {
		t.Fatalf("write satelle.toml: %v", err)
	}
}

// TestHookGateLocksSubstrateWithNoStory drives the real binary to prove that,
// with NO story engaged, the edit gate refuses authored substrate under .satelle/
// although init seeds it into edit_exempt_paths: a locked path is never exempt
// (sty_66abc0e3). Satelle-managed .gitignore stays exempt (seeded, sty_f115e6bf),
// and in-repo code stays gated.
func TestHookGateLocksSubstrateWithNoStory(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// No story engaged. Authored substrate under .satelle/ is REFUSED: the default
	// lock (.satelle/) is judged before the seeded exemption …
	for _, sub := range []string{
		filepath.Join(repo, ".satelle", "skills", "plan.md"),
		filepath.Join(repo, ".satelle", "workflows", "done.toml"),
		filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
	} {
		if gateEvent(t, repo, sub) {
			t.Errorf("edit gate allowed locked substrate %s with no story engaged (lock precedes the seeded exemption)", sub)
		}
	}
	// … satelle-managed .gitignore is ALLOWED (seeded exempt, sty_f115e6bf) …
	if !gateEvent(t, repo, filepath.Join(repo, ".gitignore")) {
		t.Error("edit gate blocked .gitignore with no engaged story (seeded exempt)")
	}
	// … in-repo CODE is still BLOCKED (needs an engaged story) …
	if gateEvent(t, repo, filepath.Join(repo, "internal", "cli", "cmd_hook.go")) {
		t.Error("edit gate allowed an in-repo code edit with no engaged story")
	}
	// Sibling REPO path is REFUSED (foreign-tree fence, sty_a8454d10).
	// Non-repo temp is not fenced by containment; with no engaged story the
	// engaged-story rule may still deny — that is a separate gate. Pin foreign
	// via a real sibling git tree.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	siblingRepo := foreignRepoDir(t)
	sibling := filepath.Join(siblingRepo, "main.go")
	if gateEvent(t, repo, sibling) {
		t.Error("edit gate allowed a sibling-repo path (must refuse foreign-tree edits)")
	}
}

// TestHookGateExemptsConfiguredPaths drives the real binary to prove exemption is
// CONFIG-only (sty_8c3d345c): a path the binary does NOT deploy is CODE — blocked —
// until a repo lists it in [gate] edit_exempt_paths, after which its edits are exempt
// while in-repo code stays gated, all with NO story engaged. This is AC3's before/after.
//
// The BEFORE case deliberately uses a harness dir satelle does not write. The
// seeded default covers satelle's OWN deployed footprint (.gitignore, .claude/,
// .grok/) because the binary writes those unasked (sty_926cfcdc) — so
// picking one of them here would test the seed, not the opt-in mechanism.
func TestHookGateExemptsConfiguredPaths(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	vendorSkill := filepath.Join(repo, ".cursor", "rules", "audit.md")
	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")

	// BEFORE: init seeds .satelle/ plus satelle's own footprint — not .cursor/,
	// which satelle never writes — so a .cursor/ edit is in-repo code, blocked
	// (no story engaged). The binary stays CLI-vendor-neutral by default.
	if gateEvent(t, repo, vendorSkill) {
		t.Error("edit gate allowed a .cursor/ edit before it was configured exempt (should be blocked as code)")
	}

	// A repo opts .cursor/ in (keeping .satelle/) via satelle.toml config.
	setExemptPaths(t, repo, ".satelle/", ".cursor/")

	// AFTER: the .cursor/ edit is now exempt (configured, not code) …
	if !gateEvent(t, repo, vendorSkill) {
		t.Error("edit gate blocked a .cursor/ edit after [gate] edit_exempt_paths opted it in (should be exempt)")
	}
	// … a configured .satelle/ prefix does not exempt locked substrate …
	if gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Error("edit gate allowed locked substrate under a configured .satelle/ prefix with no story engaged (lock precedes exemption)")
	}
	// … and in-repo CODE outside the exempt prefixes stays BLOCKED — including
	// a harness path the seeded default WOULD have exempted, because the
	// operator's narrowed list is now the only answer.
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed an in-repo code edit with no engaged story")
	}
	if gateEvent(t, repo, filepath.Join(repo, ".claude", "settings.json")) {
		t.Error("edit gate allowed .claude/ after the repo narrowed edit_exempt_paths (config decides, not the binary)")
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
	// A relative SUBSTRATE path is locked like the absolute one, seeded exemption
	// or not (sty_66abc0e3).
	if gateEventRaw(t, repo, `{"tool_input":{"file_path":".satelle/skills/plan.md"}}`) {
		t.Error("edit gate allowed a relative .satelle/ substrate path with no story engaged (locked)")
	}
}

// TestHookGateConfigDecidesSubstrate is the AC2 regression (sty_8c3d345c): the
// binary does NOT hardcode the data dir. With the lock opted out (empty [gate]
// lock_substrate_paths, sty_66abc0e3), the exempt list alone decides: a .satelle/
// edit is allowed under a .satelle/ prefix and DENIED with an explicitly EMPTY
// edit_exempt_paths, with no engaged story — config decides, not a Go rule.
func TestHookGateConfigDecidesSubstrate(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// .satelle/ exempt and nothing locked: the substrate edit is allowed.
	setExemptPaths(t, repo, ".satelle/")
	unlockSubstrate(t, repo)
	if !gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Fatal("edit gate blocked a .satelle/ edit under a .satelle/ exempt prefix with the lock opted out (should be allowed)")
	}
	// Empty the exempt list too: now the SAME edit is DENIED — no hardcoded data-dir exemption.
	setExemptPaths(t, repo)
	unlockSubstrate(t, repo)
	if gateEvent(t, repo, filepath.Join(repo, ".satelle", "skills", "plan.md")) {
		t.Error("edit gate allowed a .satelle/ edit with an empty edit_exempt_paths (data dir must not be hardcoded exempt)")
	}
}
