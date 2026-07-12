//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedReadOnlyIsOndemand proves satelle-generated-readonly is ondemand
// after the context diet (sty_cd5e341c): authored without principles:session,
// reindexed, discoverable via doc get, and NOT auto-injected by hook context.
// (sty_0b61abe5 originally session-injected the rule; order:4 demoted it — the
// 0o444 mode self-enforces hand-edits.)
func TestGeneratedReadOnlyIsOndemand(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "principles", "satelle-generated-readonly.md"),
		"---\nname: satelle-generated-readonly\ntype: principle\ntags: [type:principle]\napplies_to: [\"*\"]\ndescription: generated OKF views are read-only.\n---\n\n# Generated OKF views are read-only\n\nThe store is the source of truth; never hand-edit a generated view.\n")
	mustRun(t, testBin, repo, "reindex")

	got := mustRun(t, testBin, repo, "doc", "get", "principles", "satelle-generated-readonly")
	if !strings.Contains(got, "Generated OKF views are read-only") {
		t.Errorf("ondemand principle must still be discoverable via doc get:\n%s", got)
	}

	out := mustRun(t, testBin, repo, "hook", "context")
	if strings.Contains(out, "Generated OKF views are read-only") || strings.Contains(out, "never hand-edit a generated view") {
		t.Errorf("generated-readonly must NOT be auto-injected after context diet:\n%s", out)
	}
}
