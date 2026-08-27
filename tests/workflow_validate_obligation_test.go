//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowValidateRefusesObligationThatNamesNoStep (sty_5d712bc5):
// `satelle workflow validate` must join done.toml + step.toml and FAIL when a
// category obligation is a step STATUS (e.g. in_progress) rather than a table
// KEY. The control arm is an unmodified init'd repo, whose embedded route
// resolves.
func TestWorkflowValidateRefusesObligationThatNamesNoStep(t *testing.T) {
	t.Run("control: seeded repo validates", func(t *testing.T) {
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		materializeDefault(t, repo, "workflows", "done")
		materializeDefault(t, repo, "workflows", "step")
		out := mustRun(t, testBin, repo, "workflow", "validate")
		if strings.Contains(out, "FAIL  workflows (route)") {
			t.Errorf("legal seeded route must not fail the join check:\n%s", out)
		}
	})

	t.Run("fail: obligation is a status", func(t *testing.T) {
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		// Authored done.toml + embedded step.toml is the mixed pair GoverningWorkflows
		// overlays. The embedded catalogue keys raised/coded/closed; in_progress is
		// a STATUS several steps declare, not a table key.
		writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "Fixture whose wildcard obligations list a status instead of a step table key."

["*"]
obligations = ["raised", "in_progress", "closed"]
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
`)
		out, err := run(t, testBin, repo, "workflow", "validate")
		if err == nil {
			t.Fatalf("validate must fail when an obligation names a status:\n%s", out)
		}
		for _, want := range []string{"in_progress", "*", "has no discharging step"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})
}
