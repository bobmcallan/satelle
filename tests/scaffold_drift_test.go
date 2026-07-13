//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldDriftSurfacesAndHeal (sty_ac25b787): a repo with previous-generation
// inline PreToolUse wrappers against the current binary reports drift on
// status + hook context, fails closed on store-backed verbs, and is clean after init.
func TestScaffoldDriftSurfacesAndHeal(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Plant legacy inline gate (pre-script-file form) — the observed Grok skip shape.
	legacy := `{
  "hooks": {
    "SessionStart": [
      { "hooks": [
        { "type": "command", "command": "satelle reindex" },
        { "type": "command", "command": "satelle hook context" }
      ] }
    ],
    "PreToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit|NotebookEdit",
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
	claudePath := filepath.Join(repo, ".claude", "settings.json")
	if err := os.WriteFile(claudePath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove scripts so the deploy looks fully previous-generation.
	_ = os.RemoveAll(filepath.Join(repo, ".satelle", "hooks"))

	// AC3: status reports drift (status is exempt from fail-closed).
	st := mustRun(t, testBin, repo, "status")
	if !strings.Contains(st, "scaffold") || !strings.Contains(st, "DRIFT") {
		t.Fatalf("status must report scaffold DRIFT:\n%s", st)
	}
	if !strings.Contains(st, "satelle init") {
		t.Fatalf("status must name heal:\n%s", st)
	}

	// AC2: hook context surfaces warning (fail-open).
	ctx := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(ctx, "scaffolding drifts") && !strings.Contains(ctx, "satelle init") {
		// emitAdditionalContext wraps content — look for heal or DRIFT wording.
		if !strings.Contains(ctx, "init") {
			t.Fatalf("hook context must surface scaffold drift / heal:\n%s", ctx)
		}
	}

	// AC4: store-backed verb fails closed via hash mechanism (not ### Breaking).
	out, err := run(t, testBin, repo, "story", "list")
	if err == nil {
		t.Fatalf("story list must fail closed on scaffold drift; out=%s", out)
	}
	combined := out + err.Error()
	if !strings.Contains(combined, "satelle init") {
		t.Errorf("refuse must name satelle init: %s", combined)
	}
	if !strings.Contains(combined, "scaffold") && !strings.Contains(combined, "stale") {
		t.Errorf("refuse must mention scaffold/stale: %s", combined)
	}

	// Heal.
	mustRun(t, testBin, repo, "init")
	st2 := mustRun(t, testBin, repo, "status")
	if !strings.Contains(st2, "scaffold") || !strings.Contains(st2, "clean") {
		t.Fatalf("healed status must report scaffold clean:\n%s", st2)
	}
	if _, err := run(t, testBin, repo, "story", "list"); err != nil {
		t.Fatalf("healed story list must work: %v", err)
	}
}
