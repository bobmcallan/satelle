package agentcli

import (
	"path/filepath"
	"strings"
	"testing"
)

// sty_c4b92c9e: a driver snapshot carries the session's count of model requests
// — the unit a gate wait is judged in — or an adapter-named reason it cannot.

// grok: usage.json's session.modelCalls, with each turn's own count in the
// breakdown, so a late catch-up can credit one turn's calls exactly.
func TestGrokDriverSnapshot_ModelCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	repoRoot := "/home/example/Development/satelle"
	path := filepath.Join(home, "sessions", grokRepoDirName(repoRoot), "sess-grok-mc", "usage.json")

	writeAt(t, path, readFixture(t, "grok_usage_t1.json"))
	a := SessionUsageSnapshot(HarnessGrok, "sess-grok-mc", repoRoot)
	if a.ModelCallsUnavailableReason != "" || a.ModelCalls != 6 {
		t.Fatalf("t1: ModelCalls=%d reason=%q, want 6 (the fixture's session.modelCalls)", a.ModelCalls, a.ModelCallsUnavailableReason)
	}

	writeAt(t, path, readFixture(t, "grok_usage_t2.json"))
	b := SessionUsageSnapshot(HarnessGrok, "sess-grok-mc", repoRoot)
	if b.ModelCalls != 8 {
		t.Fatalf("t2: ModelCalls = %d, want 8", b.ModelCalls)
	}
	if len(b.TurnBreakdown) != 2 || b.TurnBreakdown[0].ModelCalls != 6 || b.TurnBreakdown[1].ModelCalls != 2 {
		t.Fatalf("t2 turn breakdown = %+v, want per-turn calls 6 then 2", b.TurnBreakdown)
	}
	if b.ModelCalls-a.ModelCalls != b.TurnBreakdown[1].ModelCalls {
		t.Fatalf("the cumulative delta %d must equal the second turn's own %d", b.ModelCalls-a.ModelCalls, b.TurnBreakdown[1].ModelCalls)
	}
}

// grok before its first turn flushes: nothing counted yet is a real zero.
func TestGrokDriverSnapshot_ModelCallsFirstTurnNotFlushed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	repoRoot := "/home/example/Development/satelle"
	writeAt(t, filepath.Join(home, "sessions", grokRepoDirName(repoRoot), "sess-first", "placeholder"), []byte("x"))
	snap := SessionUsageSnapshot(HarnessGrok, "sess-first", repoRoot)
	if !snap.Available || snap.ModelCalls != 0 || snap.ModelCallsUnavailableReason != "" {
		t.Fatalf("snapshot = %+v, want a measured zero before the first turn flushes", snap)
	}
}

// A grok usage.json with no modelCalls field is "unavailable", never a zero.
func TestGrokDriverSnapshot_NoModelCallsFieldIsNamedUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	repoRoot := "/home/example/Development/satelle"
	path := filepath.Join(home, "sessions", grokRepoDirName(repoRoot), "sess-old", "usage.json")
	writeAt(t, path, []byte(`{"sessionId":"sess-old","session":{"inputTokens":10,"outputTokens":2,"cachedReadTokens":0,"cacheCreationTokens":0,"totalTokens":12,"turnCount":1,"primaryModelId":"grok-4.7-build"},"turns":[]}`))
	snap := SessionUsageSnapshot(HarnessGrok, "sess-old", repoRoot)
	if !snap.Available {
		t.Fatalf("snapshot unavailable: %s", snap.UnavailableReason)
	}
	if !strings.HasPrefix(snap.ModelCallsUnavailableReason, "grok:") {
		t.Fatalf("ModelCallsUnavailableReason = %q, want a grok-named reason", snap.ModelCallsUnavailableReason)
	}
}

// claude: one message id is one request, however many transcript lines its
// content blocks were streamed as — the fixture's first two lines share an id.
func TestClaudeDriverSnapshot_ModelCallsCountsDistinctMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	repoRoot := "/home/example/Development/satelle"
	path := filepath.Join(home, "projects", claudeProjectSlug(repoRoot), "sess-claude-mc.jsonl")

	lines := strings.Split(strings.TrimRight(string(readFixture(t, "claude_session.jsonl")), "\n"), "\n")
	writeAt(t, path, []byte(strings.Join(lines[:2], "\n")+"\n"))
	a := SessionUsageSnapshot(HarnessClaude, "sess-claude-mc", repoRoot)
	if a.ModelCallsUnavailableReason != "" || a.ModelCalls != 1 {
		t.Fatalf("two chunks of one message: ModelCalls=%d reason=%q, want 1", a.ModelCalls, a.ModelCallsUnavailableReason)
	}
	writeAt(t, path, []byte(strings.Join(lines, "\n")+"\n"))
	b := SessionUsageSnapshot(HarnessClaude, "sess-claude-mc", repoRoot)
	if b.ModelCalls != 2 {
		t.Fatalf("ModelCalls = %d, want 2 (a second distinct message)", b.ModelCalls)
	}
}

// codex's rollout carries no request count: adapter-named unavailable.
func TestCodexDriverSnapshot_ModelCallsIsNamedUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessionID := "01a0e225-973d-7f11-90e8-68681c9d6d67"
	writeAt(t, filepath.Join(home, "sessions", "2026", "09", "27", "rollout-2026-09-27T19-15-09-"+sessionID+".jsonl"), readFixture(t, "codex_rollout.jsonl"))
	snap := SessionUsageSnapshot(HarnessCodex, sessionID, "")
	if !snap.Available {
		t.Fatalf("snapshot unavailable: %s", snap.UnavailableReason)
	}
	if !strings.HasPrefix(snap.ModelCallsUnavailableReason, "codex:") {
		t.Fatalf("ModelCallsUnavailableReason = %q, want a codex-named reason, not a zero count", snap.ModelCallsUnavailableReason)
	}
}
