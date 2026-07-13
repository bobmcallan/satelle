package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectScaffoldDrift_CleanAfterWrite(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	// Materialise canonical settings for both harnesses.
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), buildClaudeHookSettings(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".grok", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(grokHooksRel)), buildGrokHookSettings(), 0o644); err != nil {
		t.Fatal(err)
	}
	if fs := DetectScaffoldDrift(repo); len(fs) != 0 {
		t.Fatalf("canonical deploy must be clean: %v", fs)
	}
}

func TestDetectScaffoldDrift_LegacyInlineCommand(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Previous-generation inline wrapper (sty_c75c73ed) — the vire incident shape.
	legacy := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          { "type": "command", "command": "sh -c '#satelle-failvisible\nfor c in \"$HOME/.local/bin/satelle\"; do :; done; satelle hook gate'" }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := DetectScaffoldDrift(repo)
	if len(fs) == 0 {
		t.Fatal("legacy inline gate must report drift")
	}
	joined := ""
	for _, f := range fs {
		joined += f.Kind + " " + f.Path + " " + f.Detail + "\n"
	}
	if !strings.Contains(joined, "command") {
		t.Fatalf("want command drift, got:\n%s", joined)
	}
	if !strings.Contains(joined, "missing") {
		t.Fatalf("want missing script, got:\n%s", joined)
	}
	warn := formatScaffoldDriftWarning(fs)
	if !strings.Contains(warn, "satelle init") {
		t.Fatalf("warning must name heal command: %s", warn)
	}
}

func TestDetectScaffoldDrift_StaleScriptContent(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	rel := hookScriptRel("claude", "gate")
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# stale\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Settings still point at the script form so harness is "deployed".
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), buildClaudeHookSettings(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := DetectScaffoldDrift(repo)
	if len(fs) == 0 {
		t.Fatal("stale script content must drift")
	}
	found := false
	for _, f := range fs {
		if f.Path == rel && f.Kind == "content" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want content finding for %s: %v", rel, fs)
	}
}

func TestDetectScaffoldDrift_NoHarnessSkipSafe(t *testing.T) {
	repo := t.TempDir()
	// Empty repo — no settings, no scripts.
	if fs := DetectScaffoldDrift(repo); len(fs) != 0 {
		t.Fatalf("empty repo must be clean: %v", fs)
	}
}

func TestDetectScaffoldDrift_HealClears(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"satelle hook gate || exit 2"}]}]}}`
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(DetectScaffoldDrift(repo)) == 0 {
		t.Fatal("pre-heal must drift")
	}
	if _, _, err := ensureClaudeHooks(repo); err != nil {
		t.Fatal(err)
	}
	if fs := DetectScaffoldDrift(repo); len(fs) != 0 {
		t.Fatalf("post-heal must be clean: %v", fs)
	}
}
