package cli

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// TestRunRootLeavesVerbWiringUnchanged: an in-process CLI run installs the app
// it opened behind package verb's package-level wiring (stores, directories and
// the per-run resolver closures from openAppForCmd). runRoot must put every
// piece back, or the next test resolves paths into this one's temp dir.
func TestRunRootLeavesVerbWiringUnchanged(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	stubHealthz(t, false)

	restore, changed := verb.SnapshotWiring()
	t.Cleanup(restore)

	for _, args := range [][]string{{"init"}, {"status"}} {
		if out, err := runRoot(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if got := changed(); len(got) != 0 {
			t.Fatalf("after `satelle %s` verb wiring differs from the snapshot: %v", strings.Join(args, " "), got)
		}
	}

	// Control: the same command run bare does leave wiring behind — including
	// the per-run closures, which only a closure-identity comparison sees — so
	// the assertion above is not vacuous.
	t.Run("a bare Execute leaves wiring behind", func(t *testing.T) {
		root := NewRootCmd()
		resetFlagState(root)
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"status"})
		c, err := root.ExecuteC()
		if c != nil {
			closeAppForCmd(c)
		}
		if err != nil {
			t.Fatalf("status: %v\n%s", err, buf.String())
		}
		got := changed()
		for _, want := range []string{"assigneeResolver", "actorResolver", "storyDir", "workItemStore"} {
			found := false
			for _, g := range got {
				found = found || g == want
			}
			if !found {
				t.Errorf("a bare Execute left %v changed, want it to include %q", got, want)
			}
		}
		restore()
		if got := changed(); len(got) != 0 {
			t.Errorf("restore left %v changed", got)
		}
	})
}

// TestWiringSettersRestoredByHelper: a test that wires verb directly and uses
// the helper leaves the package as it found it.
func TestWiringSettersRestoredByHelper(t *testing.T) {
	_, changed := verb.SnapshotWiring()
	t.Run("sets wiring", func(t *testing.T) {
		withVerbWiring(t)
		verb.SetStoryDir("wiring-helper-sentinel")
		verb.SetAssigneeResolver(func() string { return "x" })
		if got := changed(); len(got) != 2 {
			t.Fatalf("changed() inside the subtest = %v, want storyDir and assigneeResolver", got)
		}
	})
	if got := changed(); len(got) != 0 {
		t.Fatalf("withVerbWiring left %v changed after the test ended", got)
	}
}

var verbWiringCallRE = regexp.MustCompile(`^(Set|Clear|Add)[A-Z]`)

// unguardedVerbWiring reports, as file:line strings, every use of a verb
// Set*/Clear*/Add* function (called, or passed as a value such as
// t.Cleanup(verb.ClearX)) in a top-level function that neither calls
// withVerbWiring nor verb.SnapshotWiring.
func unguardedVerbWiring(filename, src string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	verbName := ""
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "github.com/bobmcallan/satelle/internal/verb" {
			verbName = "verb"
			if imp.Name != nil {
				verbName = imp.Name.Name
			}
		}
	}
	if verbName == "" {
		return nil, nil
	}
	var out []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		guarded := false
		var uses []token.Pos
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				switch fn := v.Fun.(type) {
				case *ast.Ident:
					guarded = guarded || fn.Name == "withVerbWiring"
				case *ast.SelectorExpr:
					if x, ok := fn.X.(*ast.Ident); ok && x.Name == verbName && fn.Sel.Name == "SnapshotWiring" {
						guarded = true
					}
				}
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && x.Name == verbName && verbWiringCallRE.MatchString(v.Sel.Name) {
					uses = append(uses, v.Pos())
				}
			}
			return true
		})
		if guarded {
			continue
		}
		for _, p := range uses {
			out = append(out, fmt.Sprintf("%s:%d: %s uses verb wiring without withVerbWiring or verb.SnapshotWiring", filename, fset.Position(p).Line, fd.Name.Name))
		}
	}
	return out, nil
}

// TestVerbWiringCallsAreGuarded: a cli test that calls verb.Set*, verb.Clear*
// or verb.Add* restores the wiring through the snapshot, so it cannot leak into
// the next test.
func TestVerbWiringCallsAreGuarded(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		bad, err := unguardedVerbWiring(name, string(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, b := range bad {
			t.Error(b)
		}
	}
}

func TestVerbWiringGuardCatchesUnguarded(t *testing.T) {
	const head = "package cli\nimport (\n\t\"testing\"\n\t\"github.com/bobmcallan/satelle/internal/verb\"\n)\n"
	cases := []struct {
		name string
		body string
		want []string // line numbers expected, as ":N:"
	}{
		{"unguarded call", "func TestA(t *testing.T) {\n\tverb.SetStoryDir(\"x\")\n}\n", []string{":7:"}},
		{"unguarded clear in cleanup", "func TestA(t *testing.T) {\n\tt.Cleanup(func() { verb.ClearHoldClaimer() })\n}\n", []string{":7:"}},
		{"unguarded function value", "func TestA(t *testing.T) {\n\tt.Cleanup(verb.ClearEngagementMode)\n}\n", []string{":7:"}},
		{"unguarded add", "func TestA(t *testing.T) {\n\tverb.AddChangeNotifier(nil)\n}\n", []string{":7:"}},
		{"guarded by the helper", "func TestA(t *testing.T) {\n\twithVerbWiring(t)\n\tverb.SetStoryDir(\"x\")\n}\n", nil},
		{"guarded by SnapshotWiring", "func TestA(t *testing.T) {\n\trestore, _ := verb.SnapshotWiring()\n\tt.Cleanup(restore)\n\tverb.SetStoryDir(\"x\")\n}\n", nil},
		{"guard in a nested closure counts for the function", "func TestA(t *testing.T) {\n\tt.Run(\"s\", func(t *testing.T) {\n\t\twithVerbWiring(t)\n\t\tverb.SetStoryDir(\"x\")\n\t})\n}\n", nil},
		{"a non-wiring verb call", "func TestA(t *testing.T) {\n\t_ = verb.Dispatch\n}\n", nil},
		{"only the unguarded function is reported", "func TestA(t *testing.T) {\n\twithVerbWiring(t)\n\tverb.SetStoryDir(\"x\")\n}\nfunc TestB(t *testing.T) {\n\tverb.SetDataDir(\"x\")\n}\n", []string{":11:"}},
	}
	for _, c := range cases {
		got, err := unguardedVerbWiring("synthetic_test.go", head+c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: reported %v, want %d finding(s)", c.name, got, len(c.want))
			continue
		}
		for i, w := range c.want {
			if !strings.Contains(got[i], "synthetic_test.go"+w) {
				t.Errorf("%s: finding %q does not name synthetic_test.go%s", c.name, got[i], w)
			}
		}
	}
}
