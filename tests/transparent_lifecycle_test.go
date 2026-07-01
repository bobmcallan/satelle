//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowWithoutDoneGateValidates drives the real binary to prove the spine
// mandate is relaxed (sty_9a139c78): a workflow whose edge into `done` carries no
// gate still validates, and a transparent edge-less `step` node (the step-summary
// declaration, marked mandatory) parses and validates clean. "If the user breaks
// the process, so be it" — the done gate is the author's choice, not a mandate.
func TestWorkflowWithoutDoneGateValidates(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	wf := "---\nname: satelle-project-workflow\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: minimal lifecycle with no mandated done gate and a transparent step node\n---\n" + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  step        [agent=reviewer, prompt="@skill:satelle-step-summary", mandatory=true]
  done        [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex", "--validate=false")

	out := mustRun(t, testBin, repo, "validate", "workflows", "satelle-project-workflow")
	if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("a workflow without a done gate + a transparent step node should validate clean:\n%s", out)
	}
}
