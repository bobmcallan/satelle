//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookContextInjectsOnlyOperatingPrinciple drives the real binary to prove
// the SessionStart resident set is exactly ONE principle — the operating
// principle satelle-agent-goals (sty_53a4233c). A project principle tagged
// principles:always must NOT be auto-injected; it is resolvable on demand only.
func TestHookContextInjectsOnlyOperatingPrinciple(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A project principle that, under the OLD tag-based model, would have been
	// injected. It must now be excluded from the resident set.
	belief := "---\nname: my-belief\ntype: principle\ntags: [type:principle, principles:always]\n---\n\n# My belief\n\nThis project belief must NOT be auto-injected at session start."
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "principles", "my-belief.md"), []byte(belief), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "index", "--validate=false")

	out := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(out, "satelle-agent-goals") {
		t.Errorf("the operating principle satelle-agent-goals must be injected:\n%s", out)
	}
	if strings.Contains(out, "my-belief") || strings.Contains(out, "must NOT be auto-injected") {
		t.Errorf("a non-operating principle was injected into the resident set:\n%s", out)
	}
}
