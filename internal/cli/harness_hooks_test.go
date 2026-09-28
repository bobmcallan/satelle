// The builder / healer / checker agreement these three paths lacked
// (sty_338a53f8): a file the BUILDER writes must be recognised as complete by
// the HEALER with no rewrite, and the completeness CHECKER must ask each harness
// only for the events that harness's scaffold installs.
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIncompleteHookEvents_PerHarness is AC3: a file the builder just wrote is
// COMPLETE for its own harness, while a genuinely truncated claude file still
// reports everything it lacks.
func TestIncompleteHookEvents_PerHarness(t *testing.T) {
	repo := t.TempDir()

	for _, tc := range []struct {
		harness string
		build   func(string) []byte
	}{
		{"claude", buildClaudeHookSettings},
		{"grok", buildGrokHookSettings},
	} {
		path := filepath.Join(repo, tc.harness+"-hooks.json")
		if err := os.WriteFile(path, tc.build(repo), 0o644); err != nil {
			t.Fatal(err)
		}
		if missing := incompleteHookEvents(path, tc.harness); len(missing) != 0 {
			t.Errorf("a %s file written by the builder must be complete, got missing=%v", tc.harness, missing)
		}
	}

	truncated := filepath.Join(repo, "settings.json")
	body := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"satelle hook context"}]}]}}`
	if err := os.WriteFile(truncated, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PreToolUse": true, "UserPromptSubmit": true, "Stop": true}
	missing := incompleteHookEvents(truncated, "claude")
	if len(missing) != len(want) {
		t.Fatalf("truncated claude file: missing=%v, want %v", missing, want)
	}
	for _, e := range missing {
		if !want[e] {
			t.Errorf("unexpected missing event %q", e)
		}
	}
}

// TestHealRecognisesBuiltScaffold is AC4: the heal path and the create path now
// share one matcher definition, so healing a freshly built scaffold is a no-op —
// no reported update, nothing incomplete, and the bytes untouched.
func TestHealRecognisesBuiltScaffold(t *testing.T) {
	for _, tc := range []struct {
		harness string
		build   func(string) []byte
	}{
		{"claude", buildClaudeHookSettings},
		{"grok", buildGrokHookSettings},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			repo := t.TempDir()
			path := filepath.Join(repo, "hooks.json")
			before := tc.build(repo)
			if err := os.WriteFile(path, before, 0o644); err != nil {
				t.Fatal(err)
			}

			updated, incomplete, err := healExistingHookFile(path, tc.harness, repo)
			if err != nil {
				t.Fatalf("heal: %v", err)
			}
			if len(updated) != 0 {
				t.Errorf("heal rewrote a freshly built scaffold: %v", updated)
			}
			if len(incomplete) != 0 {
				t.Errorf("a freshly built scaffold must be complete: %v", incomplete)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Errorf("heal changed the bytes of a freshly built scaffold:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// TestHarnessHooksIsTheOneDefinition guards the seam itself: the matchers the
// builder emits are read from harnessHooks, so a future edit to one path cannot
// silently diverge from the other.
func TestHarnessHooksIsTheOneDefinition(t *testing.T) {
	for _, h := range []string{"claude", "grok"} {
		if !harnessHooks(h).hasEvent("Stop") {
			t.Errorf("%s runs stopcheck and must expect Stop", h)
		}
	}
	// An unknown harness falls back to the claude shape, which is what every
	// caller already defaulted to.
	if harnessHooks("").gateMatcher != harnessHooks("claude").gateMatcher {
		t.Error("an unnamed harness must fall back to the claude shape")
	}
}
