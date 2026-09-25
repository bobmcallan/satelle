package config

import "testing"

func TestEmbeddedHarnessParses(t *testing.T) {
	if err := EmbeddedHarnessErr(); err != nil {
		t.Fatalf("embedded harness.toml: %v", err)
	}
	for _, h := range []string{"claude", "grok", "codex", "unknown"} {
		if EmbeddedHarness()[h].ContextLimitBytes <= 0 {
			t.Errorf("embedded default for %q has no context_limit_bytes", h)
		}
	}
}

// The limit is per-harness configuration: two harnesses resolve two limits, and
// an unrecognised harness takes the unknown default, never another provider's.
func TestContextLimitPerHarness(t *testing.T) {
	c := Config{Harness: map[string]HarnessConfig{
		"claude": {ContextLimitBytes: 4000},
		"grok":   {ContextLimitBytes: 64000},
	}}
	if got := c.ContextLimit("claude"); got != 4000 {
		t.Errorf("claude = %d, want 4000", got)
	}
	if got := c.ContextLimit("GROK"); got != 64000 {
		t.Errorf("grok = %d, want 64000", got)
	}
	want := EmbeddedHarness()["unknown"].ContextLimitBytes
	for _, h := range []string{"", "unknown", "some-future-cli"} {
		if got := c.ContextLimit(h); got != want {
			t.Errorf("ContextLimit(%q) = %d, want the unknown default %d", h, got, want)
		}
	}
	if got := c.MaxContextLimit(); got != 64000 {
		t.Errorf("MaxContextLimit = %d, want 64000", got)
	}
	// Unset repo config falls through to the embedded default.
	if got := (Config{}).ContextLimit("codex"); got != EmbeddedHarness()["codex"].ContextLimitBytes {
		t.Errorf("codex default = %d", got)
	}
}
