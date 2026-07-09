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
		"satelle hook gate",
		"satelle hook commitgate",
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
	added, updated, err = ensureGrokHooks(repo)
	if err != nil || added || len(updated) != 1 {
		t.Fatalf("reconcile: added=%v updated=%v err=%v", added, updated, err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), `"satelle index"`) || !strings.Contains(string(got), `"satelle reindex"`) {
		t.Errorf("stale not fixed:\n%s", got)
	}
	if !strings.Contains(string(got), `"keep-me"`) {
		t.Errorf("user content lost:\n%s", got)
	}
	userBody, _ := os.ReadFile(userHook)
	if string(userBody) != `{"mine":true}` {
		t.Errorf("sibling hook touched: %s", userBody)
	}
}

func TestEnsureProcessHooksBoth(t *testing.T) {
	repo := t.TempDir()
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
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.json")); err != nil {
		t.Errorf("claude hooks missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(grokHooksRel))); err != nil {
		t.Errorf("grok hooks missing: %v", err)
	}
}
