//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitScaffoldsMultiHarnessHooks proves satelle init scaffolds process hooks
// for detected harnesses end-to-end (sty_2fad11b0 / epic:harness-payload):
// pre-existing .claude + .grok dirs force both scaffolds regardless of host PATH;
// Grok matchers include native tool ids; re-init is idempotent and never touches
// a sibling user hook under .grok/hooks/.
func TestInitScaffoldsMultiHarnessHooks(t *testing.T) {
	repo := t.TempDir()
	for _, d := range []string{".claude", ".grok"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out := mustRun(t, testBin, repo, "init")
	if !strings.Contains(out, ".claude/settings.json") {
		t.Errorf("init report missing Claude hooks:\n%s", out)
	}
	if !strings.Contains(out, ".grok/hooks/satelle.json") {
		t.Errorf("init report missing Grok hooks:\n%s", out)
	}

	claudePath := filepath.Join(repo, ".claude", "settings.json")
	grokPath := filepath.Join(repo, ".grok", "hooks", "satelle.json")
	claudeBody, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("claude hooks: %v", err)
	}
	grokBody, err := os.ReadFile(grokPath)
	if err != nil {
		t.Fatalf("grok hooks: %v", err)
	}
	for _, want := range []string{"satelle hook gate", "satelle hook commitgate", "satelle reindex", "satelle hook context"} {
		if !strings.Contains(string(claudeBody), want) {
			t.Errorf("claude scaffold missing %q:\n%s", want, claudeBody)
		}
		if !strings.Contains(string(grokBody), want) {
			t.Errorf("grok scaffold missing %q:\n%s", want, grokBody)
		}
	}
	for _, want := range []string{"search_replace", "run_terminal_command"} {
		if !strings.Contains(string(grokBody), want) {
			t.Errorf("grok matchers missing native tool %q:\n%s", want, grokBody)
		}
	}

	// Sibling user hook is never opened/rewritten.
	userHook := filepath.Join(repo, ".grok", "hooks", "user-extra.json")
	if err := os.WriteFile(userHook, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeGrok, _ := os.ReadFile(grokPath)
	out2 := mustRun(t, testBin, repo, "init")
	if strings.Contains(out2, "  + .grok/hooks/satelle.json") {
		t.Errorf("re-init recreated Grok hooks:\n%s", out2)
	}
	afterGrok, _ := os.ReadFile(grokPath)
	if string(beforeGrok) != string(afterGrok) {
		t.Error("re-init rewrote satelle-owned Grok hooks without stale commands")
	}
	userBody, _ := os.ReadFile(userHook)
	if string(userBody) != `{"mine":true}` {
		t.Errorf("sibling .grok/hooks file was touched: %s", userBody)
	}
}

// TestInitDefaultHooksClaudeOnlyWhenNoHarnessSignal: with PATH stripped of
// claude/grok and no harness dirs, init defaults to Claude hooks only (backward
// compatible) — not a silent no-op, and not a surprise Grok scaffold.
func TestInitDefaultHooksClaudeOnlyWhenNoHarnessSignal(t *testing.T) {
	repo := t.TempDir()
	// PATH empty enough that lookPath cannot find claude/grok on the host.
	cmd := exec.Command(testBin, "init")
	cmd.Dir = repo
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// Claude default must land.
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.json")); err != nil {
		t.Errorf("default Claude hooks missing: %v\n%s", err, out)
	}
	// Grok must not be scaffolded without a signal.
	if _, err := os.Stat(filepath.Join(repo, ".grok", "hooks", "satelle.json")); err == nil {
		t.Errorf("Grok hooks scaffolded with no signal:\n%s", out)
	}
}
