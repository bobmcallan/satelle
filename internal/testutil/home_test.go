package testutil

import (
	"os"
	"testing"
)

func TestIsolateHomeDisablesUIPushWhenUnset(t *testing.T) {
	t.Setenv("SATELLE_SERVER_ENDPOINT", "")
	_ = os.Unsetenv("SATELLE_SERVER_ENDPOINT")
	IsolateHome(t)
	if got := os.Getenv("SATELLE_SERVER_ENDPOINT"); got != "none" {
		t.Fatalf("unset endpoint after IsolateHome = %q, want none", got)
	}
}

func TestIsolateHomePreservesChosenEndpoint(t *testing.T) {
	t.Setenv("SATELLE_SERVER_ENDPOINT", "http://127.0.0.1:9")
	IsolateHome(t)
	if got := os.Getenv("SATELLE_SERVER_ENDPOINT"); got != "http://127.0.0.1:9" {
		t.Fatalf("pre-set endpoint clobbered: %q", got)
	}
}
