//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedReadOnlyRuleInjected proves the read-only rule for generated OKF
// views rides into the session-start context as a principles:session doc
// (sty_0b61abe5): authored under .satelle/principles, reindexed, and injected by
// `satelle hook context`.
func TestGeneratedReadOnlyRuleInjected(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "principles", "satelle-generated-readonly.md"),
		"---\nname: satelle-generated-readonly\ntype: principle\ntags: [type:principle, principles:session]\napplies_to: [\"*\"]\ndescription: generated OKF views are read-only.\n---\n\n# Generated OKF views are read-only\n\nThe store is the source of truth; never hand-edit a generated view.\n")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "hook", "context")
	if !strings.Contains(out, "Generated OKF views are read-only") || !strings.Contains(out, "never hand-edit a generated view") {
		t.Errorf("the read-only rule was not injected at session start:\n%s", out)
	}
}
