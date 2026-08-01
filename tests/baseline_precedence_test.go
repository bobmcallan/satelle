//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type wfListRow struct {
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Embedded bool   `json:"embedded"`
}

func wfList(t *testing.T, repo, category string) []wfListRow {
	t.Helper()
	out := mustRun(t, testBin, repo, "workflow", "list", "--category", category)
	var rows []wfListRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse workflow list: %v\n%s", err, out)
	}
	return rows
}

// TestEmbeddedBaselinePrecedence proves the base-seeding reversal (sty_bf153cbf,
// which reverses sty_3f9a6124's embedded-only stance): a fresh repo's seeded
// satelle-baseline-workflow.md is a REAL, editable repo file and the active
// default (a); with that file absent, the embedded copy still backstops the
// order-zero Get fallback (b); and a repo's own distinctly-named wildcard
// workflow takes precedence over the fallback (c).
func TestEmbeddedBaselinePrecedence(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	materializeDefaultSolution(t, repo)
	mustRun(t, testBin, repo, "reindex")

	// (a) The seeded baseline workflow (a repo file, not embedded) is the active
	// default of a fresh repo.
	var sawBaseline bool
	for _, r := range wfList(t, repo, "feature") {
		if r.Name == "satelle-baseline-workflow" {
			sawBaseline = true
			if r.Embedded {
				t.Errorf("the seeded baseline must be a real repo file, got embedded=true")
			}
			if !r.Active {
				t.Errorf("the seeded baseline must be active for a fresh repo")
			}
		}
	}
	if !sawBaseline {
		t.Fatal("baseline workflow not listed for a fresh repo")
	}

	// (b) With the repo file removed, the embedded baseline still backstops the
	// order-zero fallback (embedded=true) and remains active — gating must keep
	// working even when a repo has no authored workflow on disk at all.
	baselinePath := filepath.Join(repo, ".satelle", "workflows", "satelle-baseline-workflow.md")
	if err := os.Remove(baselinePath); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")
	rows := wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "satelle-baseline-workflow" || !rows[0].Active || rows[0].Embedded != true {
		t.Errorf("the embedded baseline must backstop the fallback once its repo file is gone, got %+v", rows)
	}

	// (c) A repo's own, distinctly-named wildcard workflow takes precedence over
	// the embedded fallback.
	// Authored inline rather than copied from this repo: the case under test is
	// PRECEDENCE among authored graphs, and this repo no longer authors any — its
	// lifecycle is a derived route (sty_9835070d).
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), repoWildcardWorkflow)
	mustRun(t, testBin, repo, "reindex")
	rows = wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "satelle-project-workflow" || !rows[0].Active {
		t.Errorf("a repo wildcard workflow must beat the embedded baseline, got %+v", rows)
	}
}

// repoWildcardWorkflow is a minimal repo-authored wildcard workflow — enough to
// out-rank the embedded baseline, and nothing more.
const repoWildcardWorkflow = `---
name: satelle-project-workflow
type: workflow
scope: project
applies_to: ["*"]
description: A repo-authored wildcard lifecycle — backlog to done, one gated edge.
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done        [shape=Msquare]
  cancelled   [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress
  in_progress -> done [agent=reviewer, prompt="@skill:satelle-story-done-review"]
  backlog -> cancelled
  in_progress -> cancelled
}
` + "```" + `
`
