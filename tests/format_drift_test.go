//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowFormatDrift_ProjectCleanAndLegacyFlagged proves
// `satelle workflow format-drift` (sty_2c0c8599): the seeded project-like
// baseline has no format lag of the kinds we care about after init when it is
// already node-consistent; a fixture with reviewer_skill= edges reports DRIFT.
func TestWorkflowFormatDrift_ProjectCleanAndLegacyFlagged(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Write a server-like lagging workflow (legacy edges + prompt-less performing).
	legacy := `---
name: satelle-legacy-server-like
type: workflow
applies_to: ["legacy-fixture"]
description: fixture for format-drift (not active for *)
---

` + "```dot" + `
digraph w {
  graph [goal="ship", vars="story"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  integration [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> integration [reviewer_skill="satelle-code-ac-review"]
  integration -> done
}
` + "```" + `
`
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-legacy-server-like.md"), legacy)
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "workflow", "format-drift", "satelle-legacy-server-like")
	if !strings.Contains(out, "DRIFT") {
		t.Fatalf("legacy fixture must report DRIFT:\n%s", out)
	}
	if !strings.Contains(out, "legacy_edge_gate") || !strings.Contains(out, "promptless_performing") {
		t.Errorf("expected legacy_edge_gate and promptless_performing:\n%s", out)
	}
	if !strings.Contains(out, "reviewer_skill") || !strings.Contains(out, "in_progress") {
		t.Errorf("findings should name the edge form and performing node:\n%s", out)
	}

	// A node-consistent workflow (write one that mirrors project conventions) is CLEAN.
	canonical := `---
name: satelle-canonical-fixture
type: workflow
applies_to: ["canonical-fixture"]
description: fixture — already on the latest format
---

` + "```dot" + `
digraph w {
  graph [goal="Drive a story to done", vars="story, repo_root"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  deploy [agent=executor, prompt="@skill:deploy"]
  done [shape=Msquare]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  in_progress -> deploy [agent=reviewer, prompt="@skill:satelle-code-ac-review"]
  deploy -> done [agent=reviewer, prompt="@skill:satelle-story-done-review"]
  deploy -> in_progress
}
` + "```" + `
`
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-canonical-fixture.md"), canonical)
	mustRun(t, testBin, repo, "reindex")
	clean := mustRun(t, testBin, repo, "workflow", "format-drift", "satelle-canonical-fixture")
	if !strings.Contains(clean, "CLEAN") {
		t.Fatalf("canonical fixture + recovery edge must be CLEAN (no format drift):\n%s", clean)
	}
	if strings.Contains(clean, "legacy_edge_gate") || strings.Contains(clean, "promptless_performing") {
		t.Errorf("must not false-positive on topology:\n%s", clean)
	}
}
