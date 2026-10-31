package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// TestNoRepoScopeMachineKeys (sty_21a7d16d AC1): Config has no web_port or
// server TOML tags — those keys are machine-scope only.
func TestNoRepoScopeMachineKeys(t *testing.T) {
	testutil.IsolateHome(t)
	rt := reflect.TypeOf(Config{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := f.Tag.Get("toml")
		name := strings.Split(tag, ",")[0]
		switch name {
		case "web_port", "server":
			t.Errorf("Config.%s still carries toml:%q — machine-scope keys must not live on repo Config", f.Name, tag)
		}
	}
}

// TestForbiddenMachineScopeIdentifiers (sty_21a7d16d AC1): ResolveWebPort and
// Config.Server appear nowhere in production Go. The identifiers are built so
// this file does not itself match.
func TestForbiddenMachineScopeIdentifiers(t *testing.T) {
	testutil.IsolateHome(t)
	resolveWebPort := "Resolve" + "WebPort"
	root := moduleRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "testdata" || base == "vendor" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file names the identifiers in comments/strings only via split
		// construction above; still skip it so a future edit cannot trip itself.
		if filepath.Base(path) == "machine_scope_test.go" {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == resolveWebPort {
					t.Errorf("%s: identifier %s must not appear", path, x.Name)
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "Server" && selectorHead(x.X) == "Config" {
					t.Errorf("%s: Config.Server must not appear", path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSettingsSchemaHasNoWebPort (sty_21a7d16d AC1): web_port is not a repo key.
func TestSettingsSchemaHasNoWebPort(t *testing.T) {
	testutil.IsolateHome(t)
	if _, ok := SettingByID("web_port"); ok {
		t.Fatal("web_port must not be a repo Settings key")
	}
	for _, s := range Settings {
		if s.Key == "web_port" || s.FieldID() == "web_port" {
			t.Fatal("Settings still lists web_port")
		}
	}
}

// TestSyncScopesStayPerRepo (sty_21a7d16d AC3): two partitions, different
// [sync] tables, ScopeFor differs; GlobalConfig has no sync field.
func TestSyncScopesStayPerRepo(t *testing.T) {
	testutil.IsolateHome(t)
	a, err := writeConfig(t, "[sync]\nskills = \"shared\"\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := writeConfig(t, "[sync]\nskills = \"personal\"\n")
	if err != nil {
		t.Fatal(err)
	}
	sa, err := ScopeFor(a, "skills")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := ScopeFor(b, "skills")
	if err != nil {
		t.Fatal(err)
	}
	if sa == sb {
		t.Fatalf("ScopeFor must differ per repo: both %v", sa)
	}
	if sa != SharedScope || sb != PersonalScope {
		t.Fatalf("scopes = %v, %v", sa, sb)
	}
	if _, ok := reflect.TypeOf(GlobalConfig{}).FieldByName("Sync"); ok {
		t.Fatal("GlobalConfig must not have a Sync field")
	}
}

func moduleRoot(t *testing.T) string {
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
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func selectorHead(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	default:
		return ""
	}
}
