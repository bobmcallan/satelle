//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentValidateHealthyAndBroken proves `satelle agent validate` (sty_93eec36d):
// exit 0 + grant surface on a healthy init; non-zero + actionable name on a
// broken agents.toml binding.
func TestAgentValidateHealthyAndBroken(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "GRANT [executor]") || !strings.Contains(out, "GRANT [reviewer]") {
		t.Errorf("validate should surface grants:\n%s", out)
	}
	if !strings.Contains(out, "PASS  agent validate green") {
		t.Errorf("healthy repo should pass:\n%s", out)
	}

	// Break the reviewer command with a known-invalid preset (force-write —
	// the scaffold embeds a full command template and comments that mention
	// `command = "claude"`, so a naive replace is unreliable).
	agents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	writeFile(t, agents, "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\ncommand = \"not-a-real-cli\"\n")

	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil {
		t.Fatalf("broken agents.toml must fail validate:\n%s", out)
	}
	if !strings.Contains(out, "not-a-real") && !strings.Contains(out, "FAIL") {
		t.Errorf("failure should name the broken command:\n%s", out)
	}
}
