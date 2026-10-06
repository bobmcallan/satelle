package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoFileStoreClientOutsideLoginLogout (sty_6ed6318d AC1): every hosted
// client the CLI builds goes through hosted.DefaultStore, so SATELLE_TOKEN
// overrides the stored login everywhere. A hosted.FileStore{} literal in a
// non-test file is allowed only inside the login and logout functions, which
// manage the file credential itself.
func TestNoFileStoreClientOutsideLoginLogout(t *testing.T) {
	allowed := map[string]bool{"runLogin": true, "runLogout": true, "runLogoutPruneLoopback": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			encl := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				encl = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "FileStore" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "hosted" && !allowed[encl] {
					t.Errorf("%s: hosted.FileStore{} in %q — use hosted.DefaultStore() so SATELLE_TOKEN applies",
						fset.Position(lit.Pos()), encl)
				}
				return true
			})
		}
	}
}
