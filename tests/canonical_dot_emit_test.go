//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReindexEmitsCanonicalNodeConsistentGates proves the ingest path
// (wfdot.ToDOT → reindex) writes the CANONICAL latest edge form
// [agent=reviewer, prompt="@skill:…"], never legacy reviewer_skill=
// (sty_ccf41efa / satelle-dot-standard).
func TestReindexEmitsCanonicalNodeConsistentGates(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Legacy inline-YAML lifecycle (still valid input) — reindex must convert
	// and rewrite the file to canonical DOT.
	yamlWF := `---
name: satelle-legacy-yaml-workflow
type: workflow
applies_to: ["feature"]
description: fixture for canonical ToDOT emit
---

# Legacy YAML lifecycle (converted on ingest)

` + "```yaml" + `
states:
  - backlog
  - {name: in_progress, agent: executor}
  - done
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
  - {from: in_progress, to: done, reviewer_skill: "satelle-story-done-review"}
` + "```" + `
`
	path := filepath.Join(repo, ".satelle", "workflows", "satelle-legacy-yaml-workflow.md")
	writeFile(t, path, yamlWF)
	mustRun(t, testBin, repo, "reindex")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "```dot") {
		t.Fatalf("reindex should rewrite YAML lifecycle to a DOT block:\n%s", got)
	}
	if strings.Contains(got, "reviewer_skill=") {
		t.Errorf("canonical emit must not leave reviewer_skill= in stored DOT:\n%s", got)
	}
	if !strings.Contains(got, `agent=reviewer, prompt="@skill:satelle-story-intent-review"`) {
		t.Errorf("canonical emit must write node-consistent intent gate:\n%s", got)
	}
	if !strings.Contains(got, `agent=reviewer, prompt="@skill:satelle-story-done-review"`) {
		t.Errorf("canonical emit must write node-consistent done gate:\n%s", got)
	}
}
