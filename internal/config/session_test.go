package config

import (
	"os"
	"strconv"
	"testing"
)

// sty_2439f4fd AC1: a cursor Shell child names its session through the cursor
// adapter's environment variable, ahead of the pid lookup and behind
// SATELLE_SESSION; without it nothing changes.
func TestResolveSessionTakesTheAdapterEnvironmentID(t *testing.T) {
	const conv = "17e98b90-f524-4ba9-a53e-eddfcff87713" // testdata/cursor/21-shell-env.txt
	t.Setenv("SATELLE_HOME", t.TempDir())
	PublishSession("sess-published")

	t.Setenv(SessionEnv, "")
	t.Setenv("CURSOR_CONVERSATION_ID", "")
	if got := ResolveSession(); got != "sess-published" {
		t.Errorf("without the cursor env, ResolveSession = %q, want the published id", got)
	}

	t.Setenv("CURSOR_CONVERSATION_ID", conv)
	if got := ResolveSession(); got != conv {
		t.Errorf("with the cursor env, ResolveSession = %q, want %q", got, conv)
	}

	t.Setenv(SessionEnv, "sess-env")
	if got := ResolveSession(); got != "sess-env" {
		t.Errorf("SATELLE_SESSION must win over the adapter env, got %q", got)
	}
}

func TestPublishSessionResolvesWithoutEnv(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("CURSOR_CONVERSATION_ID", "")
	t.Setenv(SessionEnv, "")
	_ = os.Unsetenv(SessionEnv)

	if got := ResolveSession(); got != "" {
		t.Fatalf("empty channel must resolve empty, got %q", got)
	}
	PublishSession("sess-A")
	if got := ResolveSession(); got != "sess-A" {
		t.Fatalf("published id must resolve without env, got %q", got)
	}
}

func TestResolveSessionPrefersEnvOverPublished(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	PublishSession("sess-published")
	t.Setenv(SessionEnv, "sess-env")
	if got := ResolveSession(); got != "sess-env" {
		t.Fatalf("env must win, got %q", got)
	}
}

func TestPublishSessionIgnoresEmpty(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	_ = os.Unsetenv(SessionEnv)
	PublishSession("  ")
	if got := ResolveSession(); got != "" {
		t.Fatalf("blank publish must not invent an id, got %q", got)
	}
	// File for this pid must not exist as a blank stamp.
	if _, err := os.Stat(sessionPublishDir() + "/" + strconv.Itoa(os.Getpid())); !os.IsNotExist(err) {
		t.Fatalf("blank publish must not write a file: %v", err)
	}
}

func TestPublishSessionModelRoundTrip(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	model, exe, reason := ResolveSessionModel("sess-A", "in-loop")
	if model != "" || exe != "" || reason != "" {
		t.Fatalf("unpublished pair must resolve empty, got (%q, %q, %q)", model, exe, reason)
	}
	PublishSessionModel("sess-A", "in-loop", "claude-opus-5-5", "claude", "")
	model, exe, reason = ResolveSessionModel("sess-A", "in-loop")
	if model != "claude-opus-5-5" || exe != "claude" || reason != "" {
		t.Fatalf("got (%q, %q, %q), want (claude-opus-5-5, claude, \"\")", model, exe, reason)
	}
	// A different role for the same session is a distinct slot.
	if model, _, _ := ResolveSessionModel("sess-A", "orchestrator"); model != "" {
		t.Fatalf("orchestrator role must not see the in-loop publish, got %q", model)
	}
}

func TestPublishSessionModelIgnoresEmpty(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	PublishSessionModel("", "in-loop", "opus", "claude", "")
	PublishSessionModel("sess-A", "", "opus", "claude", "")
	if model, _, _ := ResolveSessionModel("sess-A", "in-loop"); model != "" {
		t.Fatalf("empty sessionID/role must not publish, got %q", model)
	}
}

func TestPublishSessionModelReasonRoundTrip(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	PublishSessionModel("sess-B", "in-loop", "unknown", "grok", "grok's hook payload carries no model, so the in-loop tier is unknown")
	model, exe, reason := ResolveSessionModel("sess-B", "in-loop")
	if model != "unknown" || exe != "grok" || reason == "" {
		t.Fatalf("got (%q, %q, %q), want (unknown, grok, non-empty reason)", model, exe, reason)
	}
}

func TestResolveSessionModelReadsLegacyTwoFieldFile(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	dir := sessionModelDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-change rows carried no third (reason) field.
	if err := os.WriteFile(dir+"/"+sessionModelFile("sess-legacy", "in-loop"), []byte("claude-opus-5-5\tclaude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model, exe, reason := ResolveSessionModel("sess-legacy", "in-loop")
	if model != "claude-opus-5-5" || exe != "claude" || reason != "" {
		t.Fatalf("got (%q, %q, %q), want (claude-opus-5-5, claude, \"\")", model, exe, reason)
	}
}
