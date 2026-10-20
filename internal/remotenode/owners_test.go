package remotenode

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The host-command spawn and the placeholder resolver each have one owner. These
// are the files that own them, and the git spawns that are not host commands.
const (
	spawnOwner   = "internal/remotenode/host.go"
	resolveOwner = "internal/remotedef/remotedef.go"
	resolveFunc  = "HostArgv"
)

// gitSpawns are the only other exec calls in the scanned packages. Each runs
// git, never a host command, and each must still pass "git" as the program.
var gitSpawns = map[string]string{
	"internal/remotenode/fetch.go":           "git fetch",
	"internal/remotetest/run.go":             "git worktree",
	"internal/remotenode/gittest/gittest.go": "git helper only tests import",
}

// resolvedNames are the identifiers that stand for a host-command placeholder.
var resolvedNames = map[string]bool{"CommandPlaceholder": true, "PlaceholderAllow": true, "PlaceholderAud": true}

var resolvedLiterals = map[string]bool{"{command}": true, "{label}": true, "{allow}": true, "{aud}": true}

// scanOwners parses each file (repo-relative slash path to source) and reports
// every host-command spawn outside spawnOwner and every placeholder resolution
// outside resolveFunc in resolveOwner. A parse, not a line match, so comments and
// the scaffold's TOML text never count.
func scanOwners(files map[string]string) ([]string, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []string
	fset := token.NewFileSet()
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, files[path], 0)
		if err != nil {
			return nil, err
		}
		gitSpawn := false
		for _, d := range f.Decls {
			fn := ""
			if fd, ok := d.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(d, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				at := fset.Position(call.Pos())
				where := fmt.Sprintf("%s:%d", path, at.Line)
				switch pkg, name := callee(call); {
				case pkg == "exec" && (name == "Command" || name == "CommandContext"):
					if path == spawnOwner {
						return true
					}
					if _, listed := gitSpawns[path]; listed {
						gitSpawn = true
						arg := 0
						if name == "CommandContext" {
							arg = 1
						}
						if arg >= len(call.Args) || stringLit(call.Args[arg]) != "git" {
							out = append(out, where+": exec."+name+" in a git-only file must run \"git\"")
						}
						return true
					}
					out = append(out, where+": exec."+name+" spawns outside remotenode Commands ("+spawnOwner+")")
				case pkg == "strings" && (name == "Replace" || name == "ReplaceAll" || name == "NewReplacer"):
					if !resolvesPlaceholder(call) || (path == resolveOwner && fn == resolveFunc) {
						return true
					}
					out = append(out, where+": strings."+name+" resolves a host-command placeholder outside remotedef."+resolveFunc)
				}
				return true
			})
		}
		if _, listed := gitSpawns[path]; listed && !gitSpawn {
			out = append(out, path+": listed as the "+gitSpawns[path]+" spawn but no longer contains it; drop it from gitSpawns")
		}
	}
	return out, nil
}

// callee is pkg.name of a call written as pkg.name(...), else empty strings.
func callee(call *ast.CallExpr) (pkg, name string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	return id.Name, sel.Sel.Name
}

func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// resolvesPlaceholder reports whether any argument of call is a placeholder
// literal or one of the placeholder constants, bare or qualified.
func resolvesPlaceholder(call *ast.CallExpr) bool {
	for _, a := range call.Args {
		if resolvedLiterals[stringLit(a)] {
			return true
		}
		switch v := a.(type) {
		case *ast.Ident:
			if resolvedNames[v.Name] {
				return true
			}
		case *ast.SelectorExpr:
			if resolvedNames[v.Sel.Name] {
				return true
			}
		}
	}
	return false
}

// repoRoot is the nearest ancestor of the working directory holding go.mod.
func repoRoot(t *testing.T) string {
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
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// remoteSources reads the non-test Go files of the remote packages and the
// remote commands, keyed by repo-relative slash path.
func remoteSources(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(rel)] = string(b)
	}
	for _, pkg := range []string{"remotedef", "remotenode", "remotepreflight", "remotetest"} {
		err := filepath.WalkDir(filepath.Join(root, "internal", pkg), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				add(path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	cmds, err := filepath.Glob(filepath.Join(root, "internal", "cli", "cmd_remote*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range cmds {
		if !strings.HasSuffix(path, "_test.go") {
			add(path)
		}
	}
	return files
}

// TestRemoteOwnersAreSingle: a host command is spawned only by remotenode
// Commands, and a host-command placeholder is resolved only by remotedef HostArgv.
func TestRemoteOwnersAreSingle(t *testing.T) {
	files := remoteSources(t, repoRoot(t))
	for _, want := range []string{spawnOwner, resolveOwner} {
		if _, ok := files[want]; !ok {
			t.Fatalf("the scan did not reach %s", want)
		}
	}
	got, err := scanOwners(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Error(v)
	}
}

// TestScanOwnersCatchesViolations plants the violations the scan exists for and
// proves each is reported, and that the legal forms are not.
func TestScanOwnersCatchesViolations(t *testing.T) {
	planted := map[string]string{
		"internal/remotenode/planted.go": `package remotenode

func f(ctx context.Context, s, c string, argv []string) {
	strings.ReplaceAll(s, remotedef.CommandPlaceholder, c)
	exec.CommandContext(ctx, argv[0])
}
`,
		"internal/cli/cmd_remote_x.go": `package cli

func g(l string) { strings.NewReplacer("{label}", l) }
`,
	}
	got, err := scanOwners(planted)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/cli/cmd_remote_x.go:3: strings.NewReplacer resolves a host-command placeholder outside remotedef.HostArgv",
		"internal/remotenode/planted.go:4: strings.ReplaceAll resolves a host-command placeholder outside remotedef.HostArgv",
		"internal/remotenode/planted.go:5: exec.CommandContext spawns outside remotenode Commands (internal/remotenode/host.go)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("planted violations:\n got %q\nwant %q", got, want)
	}

	legal := map[string]string{
		spawnOwner:                     "package remotenode\nfunc a(ctx context.Context, v []string) { exec.CommandContext(ctx, v[0]); exec.Command(v[0]) }\n",
		"internal/remotenode/fetch.go": "package remotenode\nfunc a(ctx context.Context) { exec.CommandContext(ctx, \"git\", \"fetch\") }\n",
		resolveOwner:                   "package remotedef\nfunc (d Definition) HostArgv() { strings.NewReplacer(\"{label}\", d.l, CommandPlaceholder, c) }\n",
		"internal/remotedef/wake.go":   "package remotedef\nfunc r() { strings.ReplaceAll(c, StoryPlaceholder, id); strings.ReplaceAll(c, \"{story}\", id) }\n",
	}
	if got, err := scanOwners(legal); err != nil || len(got) != 0 {
		t.Errorf("legal forms were reported: %q, %v", got, err)
	}

	bad := map[string]string{
		"internal/remotetest/run.go":   "package remotetest\nfunc a(ctx context.Context, v []string) { exec.CommandContext(ctx, v[0]) }\n",
		"internal/remotenode/fetch.go": "package remotenode\nfunc a() {}\n",
		resolveOwner:                   "package remotedef\nfunc other(d Definition) { strings.ReplaceAll(c, PlaceholderAllow, a) }\n",
	}
	got, err = scanOwners(bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("want a non-git spawn in a git file, a stale git entry and a second resolver reported; got %q", got)
	}
}
