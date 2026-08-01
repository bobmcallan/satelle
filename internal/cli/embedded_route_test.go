package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// embeddedRouteFor derives the route the binary ships for a category — the
// lifecycle a repo with no substrate of its own is governed by (sty_3795e7f6).
func embeddedRouteFor(t *testing.T, category string) wfdot.Spec {
	t.Helper()
	done, ok := embeddedDefault("workflows", "done")
	if !ok {
		t.Fatal("the binary ships no workflows/done.md")
	}
	step, ok := embeddedDefault("workflows", "step")
	if !ok {
		t.Fatal("the binary ships no workflows/step.md")
	}
	spec, err := wfdot.ParseRoute(done, step, category, nil)
	if err != nil {
		t.Fatalf("derive the shipped route for %q: %v", category, err)
	}
	return spec
}

// hasGate reports whether the from→to edge is gated by skill.
func hasGate(spec wfdot.Spec, from, to, skill string) bool {
	for _, tr := range spec.Transitions {
		if tr.From != from || tr.To != to {
			continue
		}
		for _, sk := range tr.Skills {
			if sk == skill {
				return true
			}
		}
		if tr.Skill == skill {
			return true
		}
	}
	return false
}

// retiredEmbeddedWorkflows are the four DOT graphs the shipped route replaced.
var retiredEmbeddedWorkflows = []string{
	"satelle-baseline-workflow",
	"satelle-parent-workflow",
	"satelle-substrate-workflow",
	"satelle-task-workflow",
}

// TestNoSourceReachesForARetiredEmbeddedWorkflow is the deterministic inventory
// AC7 asks for: no non-test Go source may resolve one of the retired embedded
// graphs by name. Asserting it by construction ("the remaining wfdot.Parse
// callers all serve authored DOT") would go stale the first time someone
// reintroduces a by-name fallback; this fails loudly instead.
//
// Test files may still use the names as arbitrary fixture labels — they author
// their own bodies, and the name carries no mechanism there.
func TestNoSourceReachesForARetiredEmbeddedWorkflow(t *testing.T) {
	root := repoRootFromTest(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable dir is not this test's business
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "backups", "testdata", ".satelle-market":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, name := range retiredEmbeddedWorkflows {
			if strings.Contains(string(body), name) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+" names "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("non-test source still reaches for a retired embedded workflow by name:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestRebaseHelpDescribesTheShippedRoute is AC6's proof for rebase: the command
// help no longer documents resetting a converted repo as a regression, because
// redeploying the default now redeploys a route (sty_3795e7f6). A deterministic
// assertion on the shipped text, not inspection.
func TestRebaseHelpDescribesTheShippedRoute(t *testing.T) {
	long := findCommandLong(t, "rebase")
	for _, banned := range append([]string{"regression"}, retiredEmbeddedWorkflows...) {
		if strings.Contains(long, banned) {
			t.Errorf("rebase help still mentions %q — the conversion removed that caveat", banned)
		}
	}
	for _, want := range []string{"done.md", "step.md"} {
		if !strings.Contains(long, want) {
			t.Errorf("rebase help does not name %s — it must say what it redeploys", want)
		}
	}
}

// TestWorkflowsReadmeDescribesTheRoute is AC6's proof for the init seed text:
// the README init writes into .satelle/workflows describes the shipped
// representation (the two halves), not the retired DOT standard.
func TestWorkflowsReadmeDescribesTheRoute(t *testing.T) {
	readme := dirReadme["workflows"]
	if strings.Contains(readme, "DOT standard") {
		t.Error("the workflows README still describes authored DOT as the shipped form")
	}
	for _, want := range []string{"done.md", "step.md", "provides", "requires"} {
		if !strings.Contains(readme, want) {
			t.Errorf("the workflows README does not mention %q — an operator needs the grammar", want)
		}
	}
}

// findCommandLong returns a registered command's Long help by name.
func findCommandLong(t *testing.T, use string) string {
	t.Helper()
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == use {
			return c.Long
		}
	}
	t.Fatalf("command %q is not registered", use)
	return ""
}

// repoRootFromTest walks up from the package dir to the tree holding go.mod.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test working directory")
		}
		dir = parent
	}
}
