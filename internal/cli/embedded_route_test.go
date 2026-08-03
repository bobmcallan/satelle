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

// retiredDOTIdentifiers are the DOT front end and the tooling that maintained it
// (sty_d953c5d8). AC1/AC4: no non-test source may reintroduce any of them.
// Checked as identifiers, so a comment that merely NAMES the retired seam is
// fine — reintroducing a call is not.
var retiredDOTIdentifiers = []string{
	"wfdot.Parse(", "wfdot.ToDOT(", "wfdot.Refresh(",
	"wfdot.FormatDrift", "wfdot.OverFireWarnings(", "wfdot.SkillIsCodedCheck",
	"wfdot.InertCodedCheckAgentFindings(",
}

// TestNoSourceParsesDOT is AC4: no DOT text is parsed anywhere in the tree. The
// front end is gone, so its reappearance is a regression this fails on rather
// than a review someone has to remember to do.
func TestNoSourceParsesDOT(t *testing.T) {
	var offenders []string
	forEachNonTestGoFile(t, func(rel, body string) {
		for _, id := range retiredDOTIdentifiers {
			if strings.Contains(body, id) {
				offenders = append(offenders, rel+" calls "+id)
			}
		}
	})
	if len(offenders) > 0 {
		t.Errorf("the DOT front end is retired; these reintroduce it:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// forEachNonTestGoFile walks the tree's non-test Go sources.
func forEachNonTestGoFile(t *testing.T, fn func(rel, body string)) {
	t.Helper()
	root := repoRootFromTest(t)
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
		rel, _ := filepath.Rel(root, path)
		fn(rel, string(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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

// TestRebaseHelpDescribesTheShippedRoute: the command help no longer documents
// resetting a converted repo as a regression (sty_3795e7f6), AND it no longer
// promises a redeploy it does not perform (sty_cc550a88). A command that lies
// about its own effect is the defect, so this asserts the shipped text
// deterministically rather than by inspection.
func TestRebaseHelpDescribesTheShippedRoute(t *testing.T) {
	long := findCommandLong(t, "rebase")
	for _, banned := range append([]string{"regression", "REDEPLOYS"}, retiredEmbeddedWorkflows...) {
		if strings.Contains(long, banned) {
			t.Errorf("rebase help still mentions %q — the conversion removed that caveat", banned)
		}
	}
	// It must instead say the defaults govern from the binary, and point at the
	// verb that materializes one deliberately.
	// Phrases kept short so a line wrap in the help text cannot break the check.
	for _, want := range []string{"does NOT copy the", "IS the default solution", "satelle substrate edit"} {
		if !strings.Contains(long, want) {
			t.Errorf("rebase help does not say %q — an operator must know the wipe IS the reset", want)
		}
	}
}

// TestWorkflowsReadmeDescribesTheRoute is AC6's proof for the init seed text:
// the README init writes into .satelle/workflows describes the shipped
// representation (the two halves), not the retired DOT standard.
func TestWorkflowsReadmeDescribesTheRoute(t *testing.T) {
	readme := dirReadme["workflows"]
	// The DOT front end is retired, so ANY of its vocabulary in the text a fresh
	// repo is seeded with would teach a format the binary cannot read. Banning
	// only the phrase "DOT standard" let the rest of it through.
	low := strings.ToLower(readme)
	for _, banned := range []string{"dot standard", "digraph", ".dot", "mdiamond", "msquare", "edge csv"} {
		if strings.Contains(low, banned) {
			t.Errorf("the seeded workflows README carries retired DOT vocabulary %q", banned)
		}
	}
	// Syntax the reader gets from TOML is not this README's to teach (AC4,
	// sty_81bb0dde). The retired grammar's markers are banned outright: a seeded
	// README that still shows `## <step>` sections teaches a format the parser
	// cannot read, which is worse than teaching nothing.
	for _, banned := range []string{"## <", "## gate", "<!--", "one line"} {
		if strings.Contains(readme, banned) {
			t.Errorf("the seeded workflows README carries retired format tuition %q", banned)
		}
	}
	for _, want := range []string{
		"done.toml", "step.toml", "[[gate]]", "requires",
		// The linkage rule is the thing readers get wrong, and it is the one thing
		// TOML does not make obvious: an obligation names a step by its TABLE KEY,
		// not by the status the step declares.
		"TABLE KEY", "status",
		// The fastest post-edit feedback loop, and where the grammar is documented.
		"satelle workflow validate", "satelle help workflows",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the workflows README does not mention %q — an operator needs it", want)
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
