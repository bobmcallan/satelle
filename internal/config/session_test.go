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
