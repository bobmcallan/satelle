package config

import "testing"

func TestEmbeddedHarnessParses(t *testing.T) {
	if err := EmbeddedHarnessErr(); err != nil {
		t.Fatalf("embedded harness.toml: %v", err)
	}
	for _, h := range []string{"claude", "grok", "unknown"} {
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
	if got := (Config{}).ContextLimit("grok"); got != EmbeddedHarness()["grok"].ContextLimitBytes {
		t.Errorf("grok default = %d", got)
	}
}

// SessionContextEvent is per-harness configuration: a repo override wins, an
// unset harness is empty (no unknown-table fallback, no provider fallback), and
// the embedded grok default is the configured channel rather than a Go literal
// the test invents.
func TestSessionContextEventPerHarness(t *testing.T) {
	embedded := EmbeddedHarness()
	if embedded["grok"].SessionContextEvent == "" {
		t.Fatal("embedded grok session_context_event is unset")
	}
	if embedded["claude"].SessionContextEvent == "" || embedded["pi"].SessionContextEvent == "" {
		t.Fatal("embedded claude/pi session_context_event is unset")
	}
	if embedded["unknown"].SessionContextEvent != "" || embedded["unknown"].ToolContextLimitChars != 0 {
		t.Fatalf("[harness.unknown] must declare no event and no clip, got %+v", embedded["unknown"])
	}
	if embedded["grok"].ToolContextLimitChars <= 0 {
		t.Fatal("embedded grok tool_context_limit_chars is unset")
	}
	c := Config{Harness: map[string]HarnessConfig{
		"grok": {SessionContextEvent: "Stop", ToolContextLimitChars: 500},
	}}
	if got := c.SessionContextEvent("grok"); got != "Stop" {
		t.Errorf("repo override = %q, want Stop", got)
	}
	if got := c.ToolContextLimitChars("GROK"); got != 500 {
		t.Errorf("repo clip = %d, want 500", got)
	}
	// Unset repo key falls through to the embed, not to another provider.
	if got := (Config{}).SessionContextEvent("grok"); got != embedded["grok"].SessionContextEvent {
		t.Errorf("grok default = %q, want embedded %q", got, embedded["grok"].SessionContextEvent)
	}
	if got := (Config{}).ToolContextLimitChars("grok"); got != embedded["grok"].ToolContextLimitChars {
		t.Errorf("grok clip default = %d, want %d", got, embedded["grok"].ToolContextLimitChars)
	}
	for _, h := range []string{"", "unknown", "some-future-cli"} {
		if got := c.SessionContextEvent(h); got != "" {
			t.Errorf("SessionContextEvent(%q) = %q, want empty (no fallback)", h, got)
		}
		if got := c.ToolContextLimitChars(h); got != 0 {
			t.Errorf("ToolContextLimitChars(%q) = %d, want 0 (never grok's clip)", h, got)
		}
	}
}
