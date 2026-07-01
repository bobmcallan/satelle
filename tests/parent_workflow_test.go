//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestParentWorkflowSelectedAndValid drives the real binary: the authored
// satelle-parent-workflow validates (structure + graph) and is the ACTIVE
// workflow for BOTH container categories (epic-parent, parent), overriding the
// wildcard project workflow. The artifact under test is the repo's real workflow
// file, installed into an isolated temp repo so the assertion is hermetic.
func TestParentWorkflowSelectedAndValid(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Install the real workflow artifact from the source tree into the temp repo.
	src := filepath.Join(repoRootForTest(), ".satelle", "workflows", "satelle-parent-workflow.md")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read workflow source %s: %v", src, err)
	}
	dst := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "satelle-parent-workflow.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run("reindex"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	// validate passes: the LLM structure review is advisory with no agent
	// configured, and the graph check (backlog initial, done terminal, the spine
	// gate present) is deterministic.
	if out, err := run("workflow", "validate", "satelle-parent-workflow"); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("validate did not pass cleanly:\n%s", out)
	}

	// The new workflow is the ACTIVE (highest-priority) lifecycle for both
	// container categories, overriding the wildcard project workflow.
	type wfRow struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	for _, cat := range []string{"epic-parent", "parent"} {
		out, err := run("workflow", "list", "--category", cat)
		if err != nil {
			t.Fatalf("workflow list %s: %v\n%s", cat, err, out)
		}
		var rows []wfRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("parse workflow list %s: %v\n%s", cat, err, out)
		}
		if len(rows) == 0 || rows[0].Name != "satelle-parent-workflow" || !rows[0].Active {
			t.Errorf("category %s active workflow = %+v, want satelle-parent-workflow first/active", cat, rows)
		}
	}
}

// repoRootForTest returns the satelle repo root from this test file's location
// (tests/ -> root), so a test can read the repo's real authored substrate.
func repoRootForTest() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}
