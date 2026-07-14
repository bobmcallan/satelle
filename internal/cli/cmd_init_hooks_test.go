package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReconcileClaudeHooks covers the stale-hook reconciliation (sty_6a919dff):
// a retired satelle command inside an existing settings.json is rewritten to its
// replacement, the user's other content is preserved byte-for-byte, and the pass
// is idempotent.
func TestReconcileClaudeHooks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	stale := `{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "satelle index" },
                   { "type": "command", "command": "my-custom-hook --flag" } ] }
    ]
  },
  "custom": "user setting"
}`
	if err := os.WriteFile(p, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := reconcileClaudeHooks(p)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(changed) != 1 || !strings.Contains(changed[0], "satelle index -> satelle reindex") {
		t.Errorf("changed = %v, want the index->reindex rename", changed)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), `"satelle reindex"`) || strings.Contains(string(got), `"satelle index"`) {
		t.Errorf("stale command not rewritten:\n%s", got)
	}
	// User content preserved.
	for _, keep := range []string{"my-custom-hook --flag", `"custom": "user setting"`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("user content %q not preserved:\n%s", keep, got)
		}
	}
	// Idempotent: a second pass changes nothing.
	if changed, _ := reconcileClaudeHooks(p); len(changed) != 0 {
		t.Errorf("second pass should be a no-op, got %v", changed)
	}
	// A file with no stale commands is untouched (same bytes).
	before, _ := os.ReadFile(p)
	if _, err := reconcileClaudeHooks(p); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("clean file must be untouched")
	}
}

func TestDetectProcessHarnesses(t *testing.T) {
	repo := t.TempDir()
	// Neither CLI nor dirs → default Claude only.
	c, g := detectProcessHarnesses(repo, func(string) bool { return false })
	if !c || g {
		t.Errorf("neither: claude=%v grok=%v, want claude-only default", c, g)
	}
	// PATH signals.
	c, g = detectProcessHarnesses(repo, func(name string) bool { return name == "grok" })
	if c || !g {
		t.Errorf("grok on PATH: claude=%v grok=%v, want grok-only", c, g)
	}
	c, g = detectProcessHarnesses(repo, func(name string) bool {
		return name == "claude" || name == "grok"
	})
	if !c || !g {
		t.Errorf("both on PATH: claude=%v grok=%v, want both", c, g)
	}
	// Existing harness dirs without PATH.
	if err := os.MkdirAll(filepath.Join(repo, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, g = detectProcessHarnesses(repo, func(string) bool { return false })
	if c || !g {
		t.Errorf(".grok dir only: claude=%v grok=%v, want grok-only", c, g)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, g = detectProcessHarnesses(repo, func(string) bool { return false })
	if !c || !g {
		t.Errorf("both dirs: claude=%v grok=%v, want both", c, g)
	}
}

func TestEnsureGrokHooksCreateReconcileIdempotent(t *testing.T) {
	repo := t.TempDir()
	added, updated, _, err := ensureGrokHooks(repo)
	if err != nil || !added || len(updated) != 0 {
		t.Fatalf("create: added=%v updated=%v err=%v", added, updated, err)
	}
	path := filepath.Join(repo, filepath.FromSlash(grokHooksRel))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".satelle/hooks/pretooluse-gate-grok.sh",
		".satelle/hooks/pretooluse-commitgate-grok.sh",
		"PATH=$HOME/.local/bin:$PATH satelle hook prompt",
		"PATH=$HOME/.local/bin:$PATH satelle hook stopcheck",
		"UserPromptSubmit",
		"Stop",
		"search_replace",
		"run_terminal_command",
		"satelle reindex",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("scaffold missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(preToolUseCommands(string(body)), "$") {
		t.Errorf("PreToolUse must be $-free (sty_adfb9862):\n%s", body)
	}
	if strings.Contains(string(body), "|| exit 2") {
		t.Errorf("scaffold must not use bare '|| exit 2' (sty_c75c73ed):\n%s", body)
	}
	// Script bodies hold the multi-candidate fail-visible wrapper.
	script, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(hookScriptRel("grok", "gate"))))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{failVisibleMarker, "$HOME/.local/bin/satelle", "policy denial"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	// Second pass: no create, no reconcile.
	added, updated, _, err = ensureGrokHooks(repo)
	if err != nil || added || len(updated) != 0 {
		t.Fatalf("idempotent: added=%v updated=%v err=%v", added, updated, err)
	}
	// Stale command in satelle-owned file is reconciled; foreign content kept.
	stale := `{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "satelle index" } ] }
    ]
  },
  "custom": "keep-me"
}`
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sibling user hook is never touched.
	userHook := filepath.Join(repo, ".grok", "hooks", "user-extra.json")
	if err := os.WriteFile(userHook, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The stale file is BOTH reconciled (satelle index -> reindex) AND healed with
	// the reinforcement hooks it lacks: rename + SessionStart/PreToolUse/
	// UserPromptSubmit/Stop (sty_949e8739 + sty_0699637c).
	added, updated, incomplete, err := ensureGrokHooks(repo)
	if err != nil || added || len(updated) == 0 {
		t.Fatalf("reconcile+heal: added=%v updated=%v err=%v", added, updated, err)
	}
	if len(incomplete) > 0 {
		t.Errorf("hooks still incomplete after heal: %v", incomplete)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), `"satelle index"`) || !strings.Contains(string(got), `"satelle reindex"`) {
		t.Errorf("stale not fixed:\n%s", got)
	}
	for _, want := range []string{
		`"keep-me"`,
		"satelle hook prompt",
		"satelle hook stopcheck",
		"satelle hook context",
		"pretooluse-gate-grok.sh",
		"pretooluse-commitgate-grok.sh",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("reconcile+heal lost/omitted %q:\n%s", want, got)
		}
	}
	userBody, _ := os.ReadFile(userHook)
	if string(userBody) != `{"mine":true}` {
		t.Errorf("sibling hook touched: %s", userBody)
	}
}

func TestEnsureProcessHooksBoth(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Force both via dirs so we do not depend on the host PATH.
	for _, d := range []string{".claude", ".grok"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := ensureProcessHooks(&buf, repo); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, ".claude/settings.json") || !strings.Contains(out, grokHooksRel) {
		t.Errorf("report missing both harnesses:\n%s", out)
	}
	if !strings.Contains(out, "[compat.claude] hooks=false") {
		t.Errorf("report missing Grok compat.claude line:\n%s", out)
	}
	if !strings.Contains(out, "trusted_folders.toml") || !strings.Contains(out, "Grok project hooks trusted") {
		t.Errorf("report missing Grok folder trust line:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.json")); err != nil {
		t.Errorf("claude hooks missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(grokHooksRel))); err != nil {
		t.Errorf("grok hooks missing: %v", err)
	}
	// Grok-detected path also lands compat.claude hooks=false under HOME.
	gcfg, err := os.ReadFile(filepath.Join(home, ".grok", "config.toml"))
	if err != nil {
		t.Fatalf("grok config not written: %v", err)
	}
	if !strings.Contains(string(gcfg), "[compat.claude]") || !strings.Contains(string(gcfg), "hooks = false") {
		t.Errorf("compat.claude hooks=false missing:\n%s", gcfg)
	}
	// Folder trust for this repo root only (sty_edb01f49).
	trust, err := os.ReadFile(filepath.Join(home, ".grok", "trusted_folders.toml"))
	if err != nil {
		t.Fatalf("trusted_folders not written: %v", err)
	}
	abs, _ := filepath.Abs(repo)
	if !strings.Contains(string(trust), abs) || !strings.Contains(string(trust), "trusted = true") {
		t.Errorf("repo not trusted:\n%s", trust)
	}
	// Re-run: trust line silent (already trusted), hooks still present.
	var buf2 bytes.Buffer
	if err := ensureProcessHooks(&buf2, repo); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf2.String(), "Grok project hooks trusted") {
		t.Errorf("idempotent re-init should not re-report trust:\n%s", buf2.String())
	}
}

// TestEnsureGrokCompatConfigCreatesMissing covers AC1/AC3/AC4: absent
// ~/.grok/config.toml is created with [compat.claude] hooks = false and reported.
func TestEnsureGrokCompatConfigCreatesMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var buf bytes.Buffer
	if err := ensureGrokCompatConfig(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".grok", "config.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "[compat.claude]") || !strings.Contains(s, "hooks = false") {
		t.Errorf("expected [compat.claude] hooks=false:\n%s", s)
	}
	// Must not disable other compat facets.
	for _, ban := range []string{"skills", "rules", "agents", "mcps"} {
		if strings.Contains(s, ban) {
			t.Errorf("must not set %s; got:\n%s", ban, s)
		}
	}
	if !strings.Contains(buf.String(), "  + ") || !strings.Contains(buf.String(), "[compat.claude] hooks=false") {
		t.Errorf("create report missing:\n%s", buf.String())
	}
}

// TestEnsureGrokCompatConfigUpsertPreservesOtherKeys: surgical upsert keeps
// unrelated tables/keys and only sets hooks under [compat.claude].
func TestEnsureGrokCompatConfigUpsertPreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".grok", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `# keep me
[cli]
installer = "internal"

[compat.claude]
skills = true
hooks = true

[compat.cursor]
hooks = true
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ensureGrokCompatConfig(&buf); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	for _, keep := range []string{
		"# keep me",
		`[cli]`,
		`installer = "internal"`,
		`skills = true`,
		`[compat.cursor]`,
		`hooks = true`, // cursor still true
	} {
		if !strings.Contains(s, keep) {
			t.Errorf("lost %q:\n%s", keep, s)
		}
	}
	// claude hooks flipped to false (the assignment under [compat.claude]).
	if !strings.Contains(s, "hooks = false") {
		t.Errorf("hooks not set false:\n%s", s)
	}
	// skills must remain true (not flipped).
	if !strings.Contains(s, "skills = true") {
		t.Errorf("skills clobbered:\n%s", s)
	}
	if !strings.Contains(buf.String(), "  ~ ") || !strings.Contains(buf.String(), "[compat.claude] hooks=false") {
		t.Errorf("update report missing:\n%s", buf.String())
	}
}

// TestEnsureGrokCompatConfigAlreadyFalseIdempotent: no write, no report.
func TestEnsureGrokCompatConfigAlreadyFalseIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".grok", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "[compat.claude]\nhooks = false\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ensureGrokCompatConfig(&buf); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Errorf("file rewritten when already false:\n%s", after)
	}
	if buf.Len() != 0 {
		t.Errorf("idempotent pass must not report, got %q", buf.String())
	}
}

// TestGrokNotDetectedSkipsCompatConfig: Claude-only (no Grok signal) never
// touches ~/.grok/config.toml for this purpose (AC2/AC4).
func TestGrokNotDetectedSkipsCompatConfig(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Claude-only signal via .claude dir; no .grok, and hasCLI is forced off for
	// both by empty harness detection — but detectProcessHarnesses uses real
	// lookPath. Create .claude only so Claude is detected; Grok needs .grok or
	// grok on PATH. Host may have grok on PATH — stub lookPath to none.
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = old })

	var buf bytes.Buffer
	if err := ensureProcessHooks(&buf, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".grok", "config.toml")); err == nil {
		t.Errorf("compat config written without Grok detection:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".grok", "trusted_folders.toml")); err == nil {
		t.Errorf("folder trust written without Grok detection:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "compat.claude") {
		t.Errorf("report mentions compat.claude without Grok:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "trusted_folders") {
		t.Errorf("report mentions trusted_folders without Grok:\n%s", buf.String())
	}
}

// TestHookScaffoldFailVisible asserts PreToolUse gate/commitgate use the
// $-free script-file command (sty_adfb9862) and that script bodies carry the
// multi-candidate fail-visible wrapper (sty_c75c73ed). SessionStart stays simple.
func TestHookScaffoldFailVisible(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"claude": string(buildClaudeHookSettings()),
		"grok":   string(buildGrokHookSettings()),
	} {
		// AC1: harness command strings contain NO $ variable references.
		if strings.Contains(body, "$") {
			// SessionStart/prompt may still use $HOME in PATH=… for prompt/stopcheck
			// — only PreToolUse gate/commitgate must be $-free. Extract PreToolUse.
			if strings.Contains(preToolUseCommands(body), "$") {
				t.Errorf("%s PreToolUse command still has $ tokens:\n%s", name, body)
			}
		}
		if !strings.Contains(body, ".satelle/hooks/pretooluse-gate-"+name+".sh") {
			t.Errorf("%s missing gate script command:\n%s", name, body)
		}
		if !strings.Contains(body, ".satelle/hooks/pretooluse-commitgate-"+name+".sh") {
			t.Errorf("%s missing commitgate script command:\n%s", name, body)
		}
		if strings.Contains(body, "|| exit 2") {
			t.Errorf("%s must not use bare '|| exit 2':\n%s", name, body)
		}
		if strings.Contains(body, "sh -c ") {
			t.Errorf("%s must not use inline sh -c wrapper:\n%s", name, body)
		}
		// Script body holds marker + multi-candidate probe (not settings.json).
		script := filepath.Join(repo, filepath.FromSlash(hookScriptRel(name, "gate")))
		sb, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("%s script: %v", name, err)
		}
		for _, want := range []string{failVisibleMarker, "$HOME/.local/bin/satelle", ".satelle/satelle", "policy denial"} {
			if !strings.Contains(string(sb), want) {
				t.Errorf("%s script missing %q:\n%s", name, want, sb)
			}
		}
		// SessionStart stays bare.
		if strings.Contains(body, "PATH=$HOME/.local/bin:$PATH satelle reindex") {
			t.Errorf("%s SessionStart reindex should not be PATH-prefixed", name)
		}
	}
}

// preToolUseCommands returns a best-effort concat of PreToolUse command strings
// from a settings JSON body (test helper).
func preToolUseCommands(body string) string {
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return body
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return ""
	}
	pre, _ := hooks["PreToolUse"].([]any)
	var b strings.Builder
	for _, g := range pre {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintln(&b, hm["command"])
		}
	}
	return b.String()
}

// TestFailVisibleWrapperShell: drive the real script-file wrapper for both
// harness shapes (sty_c75c73ed AC3/AC4/AC5, sty_adfb9862).
func TestFailVisibleWrapperShell(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	// AC3: no binary → edit-gate emits infra deny JSON + exit 2.
	for _, harness := range []string{"claude", "grok"} {
		full := renderHookCommand(harness, "gate")
		if strings.Contains(full, "$") || strings.HasPrefix(full, "sh -c ") {
			t.Fatalf("command must be $-free script form: %s", full)
		}
		// Harness runs the command string from the repo root.
		c := exec.Command("sh", "-c", full)
		c.Dir = repo
		c.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
		c.Stdin = strings.NewReader(`{"tool_input":{"file_path":"x.go"}}`)
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		if err := c.Run(); err == nil {
			t.Fatalf("%s gate with no binary: want non-zero exit", harness)
		}
		got := stdout.String()
		if !strings.Contains(got, "policy denial") {
			t.Errorf("%s gate infra deny missing reason: stdout=%q stderr=%q", harness, got, stderr.String())
		}
		if harness == "claude" && !strings.Contains(got, "hookSpecificOutput") {
			t.Errorf("%s want Claude shape: %q", harness, got)
		}
		if harness == "grok" && !strings.Contains(got, `"decision":"deny"`) {
			t.Errorf("%s want Grok shape: %q", harness, got)
		}
	}

	// AC4: commitgate with no binary — echo hello allows; git commit denies.
	for _, harness := range []string{"claude", "grok"} {
		full := renderHookCommand(harness, "commitgate")
		c := exec.Command("sh", "-c", full)
		c.Dir = repo
		c.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
		c.Stdin = strings.NewReader(`{"tool_input":{"command":"echo hello"}}`)
		var stdout bytes.Buffer
		c.Stdout = &stdout
		if err := c.Run(); err != nil {
			t.Errorf("%s commitgate echo hello must allow: %v out=%q", harness, err, stdout.String())
		}
		c = exec.Command("sh", "-c", full)
		c.Dir = repo
		c.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
		c.Stdin = strings.NewReader(`{"tool_input":{"command":"git commit -m x"}}`)
		stdout.Reset()
		c.Stdout = &stdout
		if err := c.Run(); err == nil {
			t.Errorf("%s commitgate git commit must deny", harness)
		}
		if !strings.Contains(stdout.String(), "policy denial") {
			t.Errorf("%s commit deny missing reason: %q", harness, stdout.String())
		}
	}
}

// TestUpgradeFailVisibleHooks heals a legacy bare-exit-2 scaffold on re-init.
func TestUpgradeFailVisibleHooks(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2" }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2" }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	added, updated, _, err := ensureClaudeHooks(repo)
	if err != nil || added {
		t.Fatalf("heal: added=%v err=%v", added, err)
	}
	if len(updated) == 0 {
		t.Fatalf("expected fail-visible upgrade notes, got %v", updated)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), ".satelle/hooks/pretooluse-gate-claude.sh") {
		t.Fatalf("upgrade missing script-form command:\n%s", body)
	}
	if strings.Contains(string(body), "|| exit 2") {
		t.Fatalf("legacy || exit 2 still present:\n%s", body)
	}
	if strings.Contains(preToolUseCommands(string(body)), "$") {
		t.Fatalf("PreToolUse still has $ after upgrade:\n%s", body)
	}
	// Script files materialised.
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(hookScriptRel("claude", "gate")))); err != nil {
		t.Fatalf("gate script not written: %v", err)
	}
	// Idempotent second pass.
	_, updated2, _, err := ensureClaudeHooks(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range updated2 {
		if strings.Contains(u, "script-file") || strings.Contains(u, "fail-visible") {
			t.Fatalf("second pass should not re-upgrade: %v", updated2)
		}
	}
}

// TestUpgradeInlineFailVisibleToScript heals the sty_c75c73ed inline sh -c
// wrapper (with $ tokens) to the sty_adfb9862 script-file form.
func TestUpgradeInlineFailVisibleToScript(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal inline form (marker + $ + hook gate) as deployed pre-sty_adfb9862.
	inline := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          { "type": "command", "command": "sh -c '#satelle-failvisible\nb=\"\"; for c in \"$HOME/.local/bin/satelle\"; do :; done; satelle hook gate'" }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "sh -c '#satelle-failvisible\nsatelle hook commitgate'" }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(path, []byte(inline), 0o644); err != nil {
		t.Fatal(err)
	}
	_, updated, _, err := ensureClaudeHooks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) == 0 {
		t.Fatalf("expected script-file upgrade notes, got %v", updated)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "sh -c ") {
		t.Fatalf("inline sh -c still present:\n%s", body)
	}
	if strings.Contains(preToolUseCommands(string(body)), "$") {
		t.Fatalf("PreToolUse still has $:\n%s", body)
	}
	for _, sub := range []string{"gate", "commitgate"} {
		want := renderHookCommand("claude", sub)
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

// TestReinforceSessionStartAndPreToolUse (sty_0699637c): partial settings
// missing SessionStart+PreToolUse gains both on re-heal without clobbering
// existing entries.
func TestReinforceSessionStartAndPreToolUse(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Partial: only UserPromptSubmit (the pre-sty_0699637c reinforcement set).
	partial := `{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "PATH=$HOME/.local/bin:$PATH satelle hook prompt" } ] }
    ]
  },
  "custom": true
}
`
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	created, updated, incomplete, err := ensureClaudeHooks(repo)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if len(updated) == 0 {
		t.Fatalf("expected SessionStart/PreToolUse/Stop adds, got %v", updated)
	}
	if len(incomplete) > 0 {
		t.Errorf("still incomplete: %v", incomplete)
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	for _, want := range []string{
		`"custom": true`,
		"satelle hook prompt",
		"satelle hook stopcheck",
		"satelle hook context",
		"pretooluse-gate-claude.sh",
		"pretooluse-commitgate-claude.sh",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

// TestReinforceWarnsOnUnparseableSettings (sty_0699637c AC5 negative path): a
// non-object JSON root cannot be reinforced; incomplete reports unparseable and
// ensureProcessHooks emits a WARN naming it.
func TestReinforceWarnsOnUnparseableSettings(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Array root: Unmarshal into map[string]any fails → left untouched.
	if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, updated, incomplete, err := ensureClaudeHooks(repo)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if len(updated) != 0 {
		t.Fatalf("unparseable file must not be rewritten: updated=%v", updated)
	}
	if len(incomplete) == 0 {
		t.Fatal("expected incomplete non-empty for unparseable settings")
	}
	joined := strings.Join(incomplete, ",")
	if !strings.Contains(joined, "unparseable") && !strings.Contains(joined, "SessionStart") {
		t.Fatalf("incomplete = %v, want unparseable or missing events", incomplete)
	}
	// Operator-visible WARN via ensureProcessHooks.
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Force Claude-only detection.
	_ = os.MkdirAll(filepath.Join(repo, ".claude"), 0o755)
	var buf bytes.Buffer
	if err := ensureProcessHooks(&buf, repo); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "incomplete satelle hooks") {
		t.Fatalf("want incomplete WARN, got:\n%s", out)
	}
}
