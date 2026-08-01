package wfequiv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// authoredWorkflows is every workflow this repo authors. Named rather than
// globbed so a file silently disappearing fails the test instead of shrinking
// the coverage to nothing.
var authoredWorkflows = []string{
	"satelle-project-workflow.md",
	"satelle-parent-workflow.md",
	"satelle-substrate-workflow.md",
	"satelle-task-workflow.md",
}

// repoWorkflowDir walks up from the test's working directory to the tree holding
// .satelle/workflows. Returns "" when there is none, so a checkout without
// authored substrate skips rather than fails.
func repoWorkflowDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".satelle", "workflows")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// repoRoot is the tree holding .satelle/workflows — the same walk-up as
// repoWorkflowDir, returning the root rather than the workflow dir.
func repoRoot() string {
	dir := repoWorkflowDir()
	if dir == "" {
		return ""
	}
	return filepath.Dir(filepath.Dir(dir))
}

func loadAuthored(t *testing.T, name string) wfdot.Spec {
	t.Helper()
	dir := repoWorkflowDir()
	if dir == "" {
		t.Skip("no .satelle/workflows in this checkout")
	}
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	spec, ok := wfdot.Parse(string(body))
	if !ok {
		t.Fatalf("%s: no parseable dot block", name)
	}
	return spec
}

// TestAuthoredWorkflowsAreValid guards the floor: the checker must not be handed
// a broken graph and quietly agree with itself about it.
func TestAuthoredWorkflowsAreValid(t *testing.T) {
	for _, name := range authoredWorkflows {
		t.Run(name, func(t *testing.T) {
			spec := loadAuthored(t, name)
			if problems := wfdot.Validate(spec); len(problems) != 0 {
				t.Fatalf("%s does not validate: %v", name, problems)
			}
			if len(spec.States) == 0 || len(spec.Transitions) == 0 {
				t.Fatalf("%s parsed empty (states=%d transitions=%d)",
					name, len(spec.States), len(spec.Transitions))
			}
		})
	}
}

// TestAuthoredWorkflowsIdentity exercises the checker against all four authored
// workflows (AC2): identity must be empty, and a single dropped edge must be
// caught — so the identity pass is demonstrably not vacuous on that file.
func TestAuthoredWorkflowsIdentity(t *testing.T) {
	for _, name := range authoredWorkflows {
		t.Run(name, func(t *testing.T) {
			spec := loadAuthored(t, name)
			if r := Diff(spec, spec); !r.Empty() {
				t.Fatalf("%s: identity diff must be empty, got:\n%s", name, r)
			}
			mutated := spec
			mutated.Transitions = append([]wfdot.Transition(nil), spec.Transitions...)
			mutated.Transitions = mutated.Transitions[:len(mutated.Transitions)-1]
			if r := Diff(spec, mutated); r.Empty() {
				t.Fatalf("%s: dropping an edge produced no divergence — identity pass is vacuous", name)
			}
		})
	}
}
