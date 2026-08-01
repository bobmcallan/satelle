//go:build integration

package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
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

// TestEmbeddedRoutePrecedence is the end-to-end proof of the ONE precedence rule
// the shipped route introduced (sty_3795e7f6). The doc index overlays an
// embedded default wherever a repo has no file of that name, so the shipped
// done.md + step.md surface in every repo — including one that never converted.
// The rule that keeps that safe:
//
//	(a) with no authored workflow, the shipped route governs and is what a story
//	    is stamped with;
//	(b) a repo's own workflow claiming the category OUTRANKS the shipped route —
//	    upgrading the binary must not silently re-route an unconverted repo;
//	(c) a repo's own done.md + step.md outrank everything, graph or no graph.
func TestEmbeddedRoutePrecedence(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n")

	// (a) The shipped route is the active lifecycle of a fresh repo, and it is
	// what create stamps — the stamp must name what will actually gate.
	rows := wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "done.md+step.md" || !rows[0].Active {
		t.Fatalf("the shipped route must govern a fresh repo, got %+v", rows)
	}
	out := mustRun(t, testBin, repo, "story", "create", "--title", "Stamped by the shipped route",
		"--category", "feature", "--body", "Prove the stamp names the governing lifecycle.",
		"--acceptance", "1. stamped done.md+step.md")
	if !strings.Contains(out, `"workflow:done.md+step.md"`) {
		t.Errorf("create did not stamp the shipped route:\n%s", out)
	}

	// (b) A repo's own wildcard workflow OUTRANKS the shipped route. This is the
	// upgrade case: a repo that authored a graph and never converted keeps being
	// governed by that graph.
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), repoWildcardWorkflow)
	mustRun(t, testBin, repo, "reindex")
	rows = wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "satelle-project-workflow" || !rows[0].Active {
		t.Errorf("a repo workflow must outrank the shipped route, got %+v", rows)
	}
	for _, r := range rows {
		if r.Name == "done.md+step.md" {
			t.Errorf("the shipped route must not be listed while an authored workflow claims the category: %+v", rows)
		}
	}

	// …and a category the authored workflow does NOT claim still resolves to the
	// shipped route: one authored graph does not switch the whole repo off it.
	rows = wfList(t, repo, "epic-parent")
	if len(rows) == 0 || rows[0].Name != "done.md+step.md" || !rows[0].Active {
		t.Errorf("an unclaimed category must still resolve to the shipped route, got %+v", rows)
	}

	// (c) The repo's OWN route source outranks its own graph.
	seedRouteSource(t, repo)
	mustRun(t, testBin, repo, "reindex")
	rows = wfList(t, repo, "feature")
	if len(rows) == 0 || rows[0].Name != "done.md+step.md" || !rows[0].Active {
		t.Errorf("an authored route must outrank an authored workflow, got %+v", rows)
	}
}

// repoWildcardWorkflow is a minimal repo-authored wildcard workflow — enough to
// out-rank the shipped route, and nothing more. `epic-parent` is deliberately
// NOT claimed, so the unclaimed-category leg above is discriminating.
const repoWildcardWorkflow = `---
name: satelle-project-workflow
type: workflow
scope: project
applies_to: ["feature"]
description: A repo-authored lifecycle — backlog to done, one gated edge.
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
