package remotechan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// AC5 (portable): the package's production code has no way to listen — no
// net.Listen, no http server, no httptest.
func TestProductionCodeNeverListens(t *testing.T) {
	banned := map[string]map[string]bool{
		"net":  {"Listen": true, "ListenTCP": true, "ListenUnix": true, "ListenPacket": true, "ListenUDP": true},
		"http": {"ListenAndServe": true, "ListenAndServeTLS": true, "Server": true, "Serve": true},
		"tls":  {"Listen": true, "NewListener": true},
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http/httptest" {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && banned[x.Name][sel.Sel.Name] {
				t.Errorf("%s: %s.%s — the channel only dials out", fset.Position(sel.Pos()), x.Name, sel.Sel.Name)
			}
			return true
		})
	}
	if scanned < 5 {
		t.Fatalf("scanned %d production files; the scan found too little", scanned)
	}
}

// AC5 (observed): connecting both ends adds no listening socket to this
// process. Only listeners whose socket is held by this process count.
func TestNoListener(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc; TestProductionCodeNeverListens covers other platforms")
	}
	h := newHarness(t, true)
	before := ownListeners(t)
	if len(before) == 0 {
		t.Fatal("the probe sees no listener, not even the stand-in relay's")
	}

	h.connect(t)

	after := ownListeners(t)
	if len(after) != len(before) {
		t.Fatalf("listening sockets in this process went from %v to %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("listening sockets in this process went from %v to %v", before, after)
		}
	}
}

// ownListeners returns the inodes of the TCP sockets in LISTEN state that this
// process holds open.
func ownListeners(t *testing.T) []string {
	t.Helper()
	listening := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("cannot read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if i == 0 || len(fields) < 10 {
				continue
			}
			if fields[3] == "0A" { // TCP_LISTEN
				listening[fields[9]] = true
			}
		}
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	var mine []string
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		inode := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
		if listening[inode] {
			mine = append(mine, inode)
		}
	}
	sort.Strings(mine)
	return mine
}
