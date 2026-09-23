package config

import (
	"os"
	"strconv"
	"testing"
)

func TestPublishSessionResolvesWithoutEnv(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
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
	model, exe := ResolveSessionModel("sess-A", "in-loop")
	if model != "" || exe != "" {
		t.Fatalf("unpublished pair must resolve empty, got (%q, %q)", model, exe)
	}
	PublishSessionModel("sess-A", "in-loop", "claude-opus-5-5", "claude")
	model, exe = ResolveSessionModel("sess-A", "in-loop")
	if model != "claude-opus-5-5" || exe != "claude" {
		t.Fatalf("got (%q, %q), want (claude-opus-5-5, claude)", model, exe)
	}
	// A different role for the same session is a distinct slot.
	if model, _ := ResolveSessionModel("sess-A", "orchestrator"); model != "" {
		t.Fatalf("orchestrator role must not see the in-loop publish, got %q", model)
	}
}

func TestPublishSessionModelIgnoresEmpty(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	PublishSessionModel("", "in-loop", "opus", "claude")
	PublishSessionModel("sess-A", "", "opus", "claude")
	if model, _ := ResolveSessionModel("sess-A", "in-loop"); model != "" {
		t.Fatalf("empty sessionID/role must not publish, got %q", model)
	}
}
