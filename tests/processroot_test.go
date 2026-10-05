package tests

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// The suite proves the repository's LIVE authored process, so it must read that
// process from where satelle itself does. A story driven in a linked worktree
// has no .satelle of its own: satelle resolves its process of record to the
// canonical main tree's data dir (sty_ddbe2669). These helpers do the same for
// the tests, in one place — an untagged file, so `go test ./...` and
// `go test -tags integration ./tests/...` both compile them.

// moduleRoot returns the satelle module root from this file's location
// (tests/ -> root). It is the root of the CHECKOUT the tests run in — a story's
// worktree when run there — so it is right for tracked files and wrong as the
// base of an untracked data dir: use repoProcessDataDir for that.
func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

// processDataDirFor resolves the process-of-record data dir for root the way
// satelle does: the canonical main tree of root (root itself when it is not a
// linked worktree of a satelle-governed tree), then the data dir that tree's
// own config names. A missing config is the zero Config, so the default data
// dir. It only reads; it never creates a directory.
func processDataDirFor(root string) string {
	main := config.CanonicalRepoRoot(root)
	cfg, _, err := config.Load(filepath.Join(main, config.DefaultDataDir, config.ConfigName))
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, config.ErrNotFound) {
		cfg = config.Config{}
	}
	return config.ResolveProcessDataDir(cfg, main)
}

// repoProcessDataDir is the data dir holding THIS repository's authored process
// (workflows, skills, agents catalogue, substrate), whether the suite runs in
// the main tree or in a linked worktree of it.
func repoProcessDataDir(t testing.TB) string {
	t.Helper()
	return processDataDirFor(moduleRoot())
}

// moduleRootTaintSources are the calls whose result is the module root of the
// checkout — a base from which an untracked data dir must never be joined.
var moduleRootTaintSources = map[string]bool{"repoRootForTest": true, "moduleRoot": true}

// pathFuncs are the path functions through which a tainted value stays tainted.
var pathFuncs = map[string]bool{"Dir": true, "Join": true, "Abs": true, "Clean": true}

// findModuleRootDataDirJoins parses src (one Go file) and returns the lines of
// every path join that puts a data dir (".satelle" or config.DefaultDataDir)
// onto a value derived from the module root: repoRootForTest(), moduleRoot() or
// runtime.Caller, directly or through any chain of assignments and path
// functions within a function. Joins onto a t.TempDir() fixture or a parameter
// are not derived from the module root and are not reported.
func findModuleRootDataDirJoins(src string) []int {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, 0)
	if err != nil {
		return []int{-1}
	}
	var lines []int
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		tainted := map[string]bool{}
		var isTainted func(ast.Expr) bool
		isTainted = func(e ast.Expr) bool {
			switch x := e.(type) {
			case *ast.Ident:
				return tainted[x.Name]
			case *ast.ParenExpr:
				return isTainted(x.X)
			case *ast.BinaryExpr:
				return isTainted(x.X) || isTainted(x.Y)
			case *ast.CompositeLit:
				for _, el := range x.Elts {
					if isTainted(el) {
						return true
					}
				}
			case *ast.CallExpr:
				switch fn := x.Fun.(type) {
				case *ast.Ident:
					return moduleRootTaintSources[fn.Name]
				case *ast.SelectorExpr:
					pkg, _ := fn.X.(*ast.Ident)
					if pkg == nil {
						return false
					}
					if pkg.Name == "runtime" && fn.Sel.Name == "Caller" {
						return true
					}
					if (pkg.Name == "filepath" || pkg.Name == "path") && pathFuncs[fn.Sel.Name] {
						for _, a := range x.Args {
							if isTainted(a) {
								return true
							}
						}
					}
				}
			}
			return false
		}
		assign := func(lhs, rhs []ast.Expr) bool {
			changed := false
			mark := func(l ast.Expr, v bool) {
				if id, ok := l.(*ast.Ident); ok && v && id.Name != "_" && !tainted[id.Name] {
					tainted[id.Name] = true
					changed = true
				}
			}
			if len(rhs) == 1 && len(lhs) > 1 {
				for _, l := range lhs {
					mark(l, isTainted(rhs[0]))
				}
				return changed
			}
			for i, l := range lhs {
				if i < len(rhs) {
					mark(l, isTainted(rhs[i]))
				}
			}
			return changed
		}
		// Taint propagates to a fixed point, so statement order does not matter.
		for changed := true; changed; {
			changed = false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch s := n.(type) {
				case *ast.AssignStmt:
					if assign(s.Lhs, s.Rhs) {
						changed = true
					}
				case *ast.ValueSpec:
					names := make([]ast.Expr, len(s.Names))
					for i, id := range s.Names {
						names[i] = id
					}
					if assign(names, s.Values) {
						changed = true
					}
				case *ast.RangeStmt:
					if s.Value != nil && assign([]ast.Expr{s.Value}, []ast.Expr{s.X}) {
						changed = true
					}
				}
				return true
			})
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Join" {
				return true
			}
			pkg, _ := sel.X.(*ast.Ident)
			if pkg == nil || (pkg.Name != "filepath" && pkg.Name != "path") {
				return true
			}
			var onTainted, namesDataDir bool
			for _, a := range call.Args {
				if isTainted(a) {
					onTainted = true
				}
				if isDataDirName(a) {
					namesDataDir = true
				}
			}
			if onTainted && namesDataDir {
				lines = append(lines, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}
	sort.Ints(lines)
	return lines
}

// isDataDirName reports whether e names the data dir: the ".satelle" literal
// (alone or as the leading element of a slash path) or config.DefaultDataDir.
func isDataDirName(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return false
		}
		v, err := strconv.Unquote(x.Value)
		return err == nil && (v == ".satelle" || strings.HasPrefix(v, ".satelle/"))
	case *ast.SelectorExpr:
		return x.Sel.Name == "DefaultDataDir"
	}
	return false
}

// TestModuleRootJoinDetector pins the guard's own coverage: every form by which
// the suite once joined the data dir onto the module root must be caught, and
// the fixture joins that look alike must not be.
func TestModuleRootJoinDetector(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int // number of flagged joins
	}{
		{"direct call", `filepath.Join(repoRootForTest(), ".satelle", "workflows")`, 1},
		{"direct moduleRoot", `filepath.Join(moduleRoot(), ".satelle")`, 1},
		{"intermediate variable", `root := repoRootForTest()
			_ = filepath.Join(root, ".satelle", "skills", "x.md")`, 1},
		{"variable in slice literal", `root := repoRootForTest()
			_ = []string{filepath.Join(root, ".satelle"), filepath.Join(root, "internal")}`, 1},
		{"runtime.Caller chain", `_, file, _, _ := runtime.Caller(0)
			dir := filepath.Dir(filepath.Dir(file))
			_ = filepath.Join(dir, ".satelle")`, 1},
		{"runtime.Caller inline", `_, file, _, _ := runtime.Caller(0)
			_ = filepath.Join(filepath.Dir(file), ".satelle")`, 1},
		{"DefaultDataDir on tainted root", `root := moduleRoot()
			_ = filepath.Join(root, config.DefaultDataDir, "agents.toml")`, 1},
		{"reassigned through a join", `root := repoRootForTest()
			sub := filepath.Join(root, "tests")
			_ = filepath.Join(sub, ".satelle")`, 1},
		{"var declaration", `var root = repoRootForTest()
			_ = filepath.Join(root, ".satelle")`, 1},
		{"range over a tainted slice", `for _, base := range []string{repoRootForTest()} {
				_ = filepath.Join(base, ".satelle")
			}`, 1},
		{"slash path literal", `_ = filepath.Join(moduleRoot(), ".satelle/workflows")`, 1},
		{"TempDir fixture", `repo := t.TempDir()
			_ = filepath.Join(repo, ".satelle", "workflows")`, 0},
		{"function parameter", `_ = filepath.Join(root, ".satelle")`, 0},
		{"tracked file on the module root", `_ = filepath.Join(moduleRoot(), "internal", "config", "substrate")`, 0},
		{"data dir on the resolved helper", `_ = filepath.Join(repoProcessDataDir(t), "workflows")`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\nfunc f(root string) {\n" + tc.body + "\n}\n"
			if got := findModuleRootDataDirJoins(src); len(got) != tc.want {
				t.Errorf("flagged lines = %v, want %d hit(s)\n%s", got, tc.want, tc.body)
			}
		})
	}
}

// TestNoModuleRootDataDirJoin fails on any test source that joins the data dir
// onto the module root. In a story's linked worktree that join names a
// .satelle that does not exist; resolve it with repoProcessDataDir instead.
// tests/plannerbench is a separate opt-in build outside the integration suite.
func TestNoModuleRootDataDirJoin(t *testing.T) {
	root := filepath.Join(moduleRoot(), "tests")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "plannerbench" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(moduleRoot(), p)
		for _, line := range findModuleRootDataDirJoins(string(b)) {
			t.Errorf("%s:%s joins the data dir onto the module root; use repoProcessDataDir(t)", rel, fmt.Sprint(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
