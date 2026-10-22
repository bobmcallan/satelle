package agentcli

import "testing"

func TestCommandFromHookEvent(t *testing.T) {
	// Claude Code snake_case (tool_input.command).
	if got := CommandFromHookEvent([]byte(`{"tool_input":{"command":"git push"}}`)); got != "git push" {
		t.Errorf("claude CommandFromHookEvent = %q, want 'git push'", got)
	}
	// Grok camelCase (toolInput.command) — sty_0d3665ee.
	if got := CommandFromHookEvent([]byte(`{"hookEventName":"pre_tool_use","toolName":"run_terminal_command","toolInput":{"command":"git commit -m x"}}`)); got != "git commit -m x" {
		t.Errorf("grok CommandFromHookEvent = %q, want 'git commit -m x'", got)
	}
	if got := CommandFromHookEvent([]byte(`{"tool_input":{"file_path":"/a"}}`)); got != "" {
		t.Errorf("non-shell event should yield empty command, got %q", got)
	}
	if got := CommandFromHookEvent([]byte(`not json`)); got != "" {
		t.Errorf("bad event should yield empty command, got %q", got)
	}
}

func TestHarnessHooksSurface(t *testing.T) {
	if HarnessHooks("").GateMatcher != HarnessHooks(HarnessClaude).GateMatcher {
		t.Error("an unknown harness must get the claude shape")
	}
	cursor := HarnessHooks(HarnessCursor)
	if !cursor.NoToolMatcher || cursor.GateMatcher != "" || cursor.CommitMatcher != "" {
		t.Errorf("cursor spec = %+v, want NoToolMatcher with no matchers", cursor)
	}
	if !HarnessHooks("pi").HasEvent("Stop") || HarnessHooks(HarnessCursor).HasEvent("UserPromptSubmit") {
		t.Error("HasEvent disagrees with the per-harness event table")
	}
}
