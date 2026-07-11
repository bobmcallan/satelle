//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowRefresh_DryRunAndApply proves consultative refresh (sty_084f4879):
// dry-run shows a diff and writes nothing; --apply with --prompt rewrites
// legacy edges + performing prompts; re-run is a no-op.
func TestWorkflowRefresh_DryRunAndApply(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	legacy := `---
name: satelle-refresh-fixture
type: workflow
applies_to: ["refresh-fixture"]
description: lagging fixture for refresh
---

` + "```dot" + `
digraph w {
  graph [goal="ship", vars="story"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress [reviewer_skill="satelle-story-intent-review"]
  in_progress -> done
}
` + "```" + `

## Environment

` + "```yaml" + `
guardrails:
  always:
    - keep guardrails
` + "```" + `
`
	path := filepath.Join(repo, ".satelle", "workflows", "satelle-refresh-fixture.md")
	writeFile(t, path, legacy)
	mustRun(t, testBin, repo, "reindex")

	// Dry-run: no write
	out := mustRun(t, testBin, repo, "workflow", "refresh", "satelle-refresh-fixture",
		"--prompt", "in_progress=code")
	if !strings.Contains(out, "Dry-run") && !strings.Contains(out, "dry-run") {
		t.Errorf("dry-run should say so:\n%s", out)
	}
	if !strings.Contains(out, "reviewer_skill") && !strings.Contains(out, "legacy_edge") && !strings.Contains(out, "+") {
		t.Errorf("dry-run should show a proposed change:\n%s", out)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "reviewer_skill=") {
		t.Fatal("dry-run must not write the file")
	}
	if !strings.Contains(string(raw), "keep guardrails") {
		t.Fatal("fixture guardrails must still be present")
	}

	// Apply
	out = mustRun(t, testBin, repo, "workflow", "refresh", "satelle-refresh-fixture",
		"--apply", "--prompt", "in_progress=code")
	if !strings.Contains(out, "wrote") {
		t.Errorf("apply should report write:\n%s", out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "reviewer_skill=") {
		t.Errorf("apply must rewrite legacy edges:\n%s", got)
	}
	if !strings.Contains(got, `prompt="@skill:code"`) {
		t.Errorf("apply must add performing prompt:\n%s", got)
	}
	if !strings.Contains(got, "keep guardrails") {
		t.Error("guardrails must survive apply")
	}

	// Idempotent
	out = mustRun(t, testBin, repo, "workflow", "refresh", "satelle-refresh-fixture",
		"--apply", "--prompt", "in_progress=code")
	if !strings.Contains(out, "already canonical") && !strings.Contains(out, "no changes") {
		t.Errorf("second apply should be no-op:\n%s", out)
	}
}
