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

	// Install this repo's real route source — the lifecycle that governs an
	// epic-parent now that the graphs are retired (sty_9835070d).
	seedRouteSource(t, repo)

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
	// Only the declaration of done is validated here. `step` names this repo's
	// full rubric set (code, integrate, release, the deployment checks), and this
	// temp repo has only the embedded default skills — so a FAIL there would be
	// the fixture's missing substrate, not the lifecycle under test. That the
	// route source resolves its gate skills at all is covered in the real repo by
	// `satelle workflow validate`.
	if out, err := run("workflow", "validate", "done"); err != nil {
		t.Fatalf("validate done failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("validate done did not pass cleanly:\n%s", out)
	}

	// The DERIVED route is the ACTIVE lifecycle for both container categories: it
	// governs before any authored workflow is considered (sty_9835070d), and it
	// claims epic-parent and parent with sections of their own rather than
	// falling through to the wildcard.
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
		if len(rows) == 0 || rows[0].Name != "done.md+step.md" || !rows[0].Active {
			t.Errorf("category %s active workflow = %+v, want the derived route first/active", cat, rows)
		}
		// …and it is the container lifecycle, not the wildcard one: backlog closes
		// straight to done, with no plan/in_progress/integration/release step.
		spec := repoRouteSpec(t, cat, nil)
		var names []string
		for _, st := range spec.States {
			names = append(names, st.Name)
		}
		for _, absent := range []string{"plan", "in_progress", "integration", "release"} {
			if containsStrSlice(names, absent) {
				t.Errorf("category %s must have no %q step — a container has no slice of its own (states %v)", cat, absent, names)
			}
		}
		if !spec.HasEdge("backlog", "done") {
			t.Errorf("category %s must close backlog → done directly (states %v)", cat, names)
		}
	}
}

// repoRootForTest returns the satelle repo root from this test file's location
// (tests/ -> root), so a test can read the repo's real authored substrate.
func repoRootForTest() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}
