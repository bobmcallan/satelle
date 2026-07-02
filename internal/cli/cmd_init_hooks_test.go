package cli

import (
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
