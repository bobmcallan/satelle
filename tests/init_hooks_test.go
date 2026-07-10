//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runInitIsolated runs `satelle init` in repo with HOME (and TMPDIR) pointed at
// disposable dirs so Grok-compat writes cannot touch the developer's real
// ~/.grok/config.toml (sty_24b32127).
func runInitIsolated(t *testing.T, repo string) (out string, home string) {
	t.Helper()
	home = t.TempDir()
	cmd := exec.Command(testBin, "init")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"TMPDIR="+t.TempDir(),
	)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, b)
	}
	return string(b), home
}

// TestInitScaffoldsMultiHarnessHooks proves satelle init scaffolds process hooks
// for detected harnesses end-to-end (sty_2fad11b0 / epic:harness-payload):
// pre-existing .claude + .grok dirs force both scaffolds regardless of host PATH;
// Grok matchers include native tool ids; re-init is idempotent and never touches
// a sibling user hook under .grok/hooks/. Also (sty_24b32127): Grok detection
// sets [compat.claude] hooks=false in ~/.grok/config.toml and gate/commitgate
// commands carry a PATH prefix for $HOME/.local/bin.
func TestInitScaffoldsMultiHarnessHooks(t *testing.T) {
	repo := t.TempDir()
	for _, d := range []string{".claude", ".grok"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out, home := runInitIsolated(t, repo)
	if !strings.Contains(out, ".claude/settings.json") {
		t.Errorf("init report missing Claude hooks:\n%s", out)
	}
	if !strings.Contains(out, ".grok/hooks/satelle.json") {
		t.Errorf("init report missing Grok hooks:\n%s", out)
	}
	if !strings.Contains(out, "[compat.claude] hooks=false") {
		t.Errorf("init report missing Grok compat.claude line:\n%s", out)
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
	for _, want := range []string{
		"PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2",
		"PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2",
		"satelle reindex",
		"satelle hook context",
	} {
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

	gcfgPath := filepath.Join(home, ".grok", "config.toml")
	gcfg, err := os.ReadFile(gcfgPath)
	if err != nil {
		t.Fatalf("grok config not written under isolated HOME: %v", err)
	}
	if !strings.Contains(string(gcfg), "[compat.claude]") || !strings.Contains(string(gcfg), "hooks = false") {
		t.Errorf("compat.claude hooks=false missing:\n%s", gcfg)
	}

	// Sibling user hook is never opened/rewritten.
	userHook := filepath.Join(repo, ".grok", "hooks", "user-extra.json")
	if err := os.WriteFile(userHook, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeGrok, _ := os.ReadFile(grokPath)
	// Re-init with the same isolated HOME so already-false stays silent.
	cmd := exec.Command(testBin, "init")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+t.TempDir())
	b2, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("re-init: %v\n%s", err, b2)
	}
	out2 := string(b2)
	if strings.Contains(out2, "  + .grok/hooks/satelle.json") {
		t.Errorf("re-init recreated Grok hooks:\n%s", out2)
	}
	// already-false is silent (no + / ~ report for compat.claude).
	if strings.Contains(out2, "[compat.claude] hooks=false") {
		t.Errorf("re-init re-reported already-false compat.claude:\n%s", out2)
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
// compatible) — not a silent no-op, and not a surprise Grok scaffold. Also does
// not write ~/.grok/config.toml for compat.claude (AC2).
func TestInitDefaultHooksClaudeOnlyWhenNoHarnessSignal(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	// PATH empty enough that lookPath cannot find claude/grok on the host.
	cmd := exec.Command(testBin, "init")
	cmd.Dir = repo
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
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
	// No Grok → no compat.claude write.
	if _, err := os.Stat(filepath.Join(home, ".grok", "config.toml")); err == nil {
		t.Errorf("~/.grok/config.toml written without Grok detection:\n%s", out)
	}
	if strings.Contains(string(out), "compat.claude") {
		t.Errorf("report mentions compat.claude without Grok:\n%s", out)
	}
}
