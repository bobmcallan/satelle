package cli

import (
	"bytes"
	"os"
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
	added, updated, err := ensureGrokHooks(repo)
	if err != nil || !added || len(updated) != 0 {
		t.Fatalf("create: added=%v updated=%v err=%v", added, updated, err)
	}
	path := filepath.Join(repo, filepath.FromSlash(grokHooksRel))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2",
		"PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2",
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
	// Second pass: no create, no reconcile.
	added, updated, err = ensureGrokHooks(repo)
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
	// the reinforcement hooks it lacks (sty_949e8739): 1 rename + 2 added hooks.
	added, updated, err = ensureGrokHooks(repo)
	if err != nil || added || len(updated) != 3 {
		t.Fatalf("reconcile+heal: added=%v updated=%v err=%v", added, updated, err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), `"satelle index"`) || !strings.Contains(string(got), `"satelle reindex"`) {
		t.Errorf("stale not fixed:\n%s", got)
	}
	for _, want := range []string{`"keep-me"`, "satelle hook prompt", "satelle hook stopcheck"} {
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

// TestHookScaffoldPATHHardening asserts gate/commitgate command strings include
// $HOME/.local/bin on PATH (AC5). Unquoted env prefix keeps the JSON scaffold
// free of escaped quotes while still expanding under a POSIX shell.
func TestHookScaffoldPATHHardening(t *testing.T) {
	want := "PATH=$HOME/.local/bin:$PATH satelle hook"
	for name, body := range map[string]string{
		"claude": claudeHookSettings,
		"grok":   grokHookSettings,
	} {
		if !strings.Contains(body, want+" gate || exit 2") {
			t.Errorf("%s scaffold missing PATH-hardened gate:\n%s", name, body)
		}
		if !strings.Contains(body, want+" commitgate || exit 2") {
			t.Errorf("%s scaffold missing PATH-hardened commitgate:\n%s", name, body)
		}
		// SessionStart stays bare (AC5 scopes gate/commitgate only).
		if strings.Contains(body, "PATH=$HOME/.local/bin:$PATH satelle reindex") {
			t.Errorf("%s SessionStart reindex should not be PATH-prefixed", name)
		}
	}
}
