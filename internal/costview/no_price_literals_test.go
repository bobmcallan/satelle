package costview_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoPriceLiteralsInProductCode pins sty_c4df7376 AC4 / sty_6267abb7 AC2:
// a rate is configuration, never Go. It walks every non-test Go file under
// internal/ and fails on a literal that would be a built-in price — a
// string-keyed map of floats keyed by model ids (a model-id→rate table) or a
// populated Rates or Subscription value. Tests may build both freely; the product may only decode
// them from the operator's config.
func TestNoPriceLiteralsInProductCode(t *testing.T) {
	root := filepath.Join("..") // internal/
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("parse %s: %v", path, perr)
			return nil
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || len(lit.Elts) == 0 {
				return true
			}
			if isPriceLiteral(lit) {
				t.Errorf("%s: price literal %s — rates are configuration, not Go", fset.Position(lit.Pos()), exprName(lit.Type))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("scanned only %d files under %s — the walk is not reaching internal/", checked, root)
	}
}

// modelIDKey matches a literal key that names a model, as opposed to some other
// string-keyed float table (a config-validation range check, say).
var modelIDKey = regexp.MustCompile(`(?i)claude|opus|sonnet|haiku|gpt|grok|gemini|llama|mistral|qwen|deepseek|\bo[0-9]\b`)

// isPriceLiteral reports whether lit is a model-id-keyed table of floats or
// Rates, or a populated Rates / Subscription value.
func isPriceLiteral(lit *ast.CompositeLit) bool {
	if m, ok := lit.Type.(*ast.MapType); ok {
		if k, ok := m.Key.(*ast.Ident); !ok || k.Name != "string" {
			return false
		}
		if exprName(m.Value) == "Rates" {
			return true
		}
		if v, ok := m.Value.(*ast.Ident); !ok || (v.Name != "float64" && v.Name != "float32") {
			return false
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if b, ok := kv.Key.(*ast.BasicLit); ok && b.Kind == token.STRING && modelIDKey.MatchString(b.Value) {
				return true
			}
		}
		return false
	}
	switch exprName(lit.Type) {
	case "Rates", "Subscription":
		return true
	}
	return false
}

func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.MapType:
		return "map[" + exprName(x.Key) + "]" + exprName(x.Value)
	}
	return ""
}

// TestPriceLiteralDetectorFlagsWhatItShould keeps the scan from passing
// vacuously: each shape below must be flagged, and an unrelated float table not.
func TestPriceLiteralDetectorFlagsWhatItShould(t *testing.T) {
	cases := map[string]bool{
		`map[string]float64{"claude-opus-5-5": 15}`:        true,
		`map[string]Rates{"x": {}}`:                        true,
		`costview.Rates{FreshInput: 3}`:                    true,
		`Subscription{FeeUSD: 20}`:                         true,
		`map[string]float64{"first_fraction": 0.1}`:        false,
		`map[string]int{"claude-opus-5-5": 1}`:             false,
		`costview.Pricing{Models: nil, Subscription: nil}`: false,
	}
	for src, want := range cases {
		expr, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		lit := expr.(*ast.CompositeLit)
		if got := isPriceLiteral(lit); got != want {
			t.Errorf("isPriceLiteral(%s) = %v, want %v", src, got, want)
		}
	}
}
