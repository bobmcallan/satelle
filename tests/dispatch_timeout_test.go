//go:build integration

// Black-box coverage for sty_446c38b7: a per-binding dispatch timeout in
// agents.toml, validated fail-fast when the CLI loads the agents layer.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatchTimeoutFailFast pins AC1/AC4 end-to-end: a malformed [<binding>]
// timeout is rejected when the CLI loads agents.toml — a typo is caught at load,
// before any dispatch, naming the timeout.
func TestDispatchTimeoutFailFast(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	agents := filepath.Join(repo, ".satelle", "agents.toml")

	if err := os.WriteFile(agents, []byte("[worker]\ncommand = \"claude {system}\"\ntimeout = \"notaduration\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, testBin, repo, "reindex")
	if err == nil {
		t.Fatalf("a malformed binding timeout must fail fast at load:\n%s", out)
	}
	if !strings.Contains(out, "timeout") {
		t.Errorf("the load failure should name the timeout, got:\n%s", out)
	}

	// A well-formed timeout loads cleanly.
	if err := os.WriteFile(agents, []byte("[worker]\ncommand = \"claude {system}\"\ntimeout = \"45m\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, testBin, repo, "reindex"); err != nil {
		t.Fatalf("a valid binding timeout should load: %v\n%s", err, out)
	}
}
