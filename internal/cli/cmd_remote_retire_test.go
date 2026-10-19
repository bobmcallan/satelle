package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// retireStub answers every request with code and body and records them.
type retireStub struct {
	ts   *httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newRetireStub(t *testing.T, code int, body string) *retireStub {
	t.Helper()
	s := &retireStub{}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Clone()})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *retireStub) requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.seen...)
}

// retireRepo is a repo with remote "edge" defined and a session token set.
func retireRepo(t *testing.T) {
	t.Helper()
	remoteWithTriggers(t)
}

func TestRemoteRetireYesSendsOneDelete(t *testing.T) {
	retireRepo(t)
	srv := newRetireStub(t, http.StatusNoContent, "")
	out, err := runRootIn(t, "", "remote", "retire", "rmt_x", "--yes", "--server", srv.ts.URL)
	if err != nil {
		t.Fatalf("retire: %v\n%s", err, out)
	}
	rs := srv.requests()
	if len(rs) != 1 {
		t.Fatalf("want exactly one request, got %+v", rs)
	}
	if r := rs[0]; r.Method != http.MethodDelete || r.Path != "/api/v1/remotes/rmt_x" || r.Header.Get("Authorization") != "Bearer stub-token" {
		t.Errorf("sent %s %s with Authorization %q", r.Method, r.Path, r.Header.Get("Authorization"))
	}
	if strings.Contains(out, "Type 'yes'") {
		t.Errorf("--yes must not prompt:\n%s", out)
	}
}

func TestRemoteRetirePrompt(t *testing.T) {
	retireRepo(t)
	t.Run("yes confirms", func(t *testing.T) {
		srv := newRetireStub(t, http.StatusNoContent, "")
		out, err := runRootIn(t, "yes\n", "remote", "retire", "rmt_x", "--server", srv.ts.URL)
		if err != nil {
			t.Fatalf("retire: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Retire remote rmt_x?") {
			t.Errorf("the prompt must name the remote id:\n%s", out)
		}
		if n := len(srv.requests()); n != 1 {
			t.Errorf("want one request after yes, got %d", n)
		}
	})
	for _, answer := range []string{"no\n", "y\n", "YES\n", "Yes\n", "yes please\n", "\n", ""} {
		t.Run(fmt.Sprintf("declined %q", answer), func(t *testing.T) {
			srv := newRetireStub(t, http.StatusNoContent, "")
			out, err := runRootIn(t, answer, "remote", "retire", "rmt_x", "--server", srv.ts.URL)
			if err != nil {
				t.Fatalf("a decline is not an error: %v\n%s", err, out)
			}
			if !strings.Contains(out, "nothing sent") {
				t.Errorf("a decline must say nothing was sent:\n%s", out)
			}
			if rs := srv.requests(); len(rs) != 0 {
				t.Errorf("a decline must send nothing, got %+v", rs)
			}
		})
	}
}

func TestRemoteRetireOutputs(t *testing.T) {
	retireRepo(t)
	t.Run("204 names the wake secret from --def", func(t *testing.T) {
		srv := newRetireStub(t, http.StatusNoContent, "")
		out, err := runRootIn(t, "", "remote", "retire", "rmt_x", "--yes", "--def", "edge", "--server", srv.ts.URL)
		if err != nil {
			t.Fatalf("retire: %v\n%s", err, out)
		}
		for _, want := range []string{"remote rmt_x retired", "fire history is kept", "REMOTE_WAKE_KEY", "is not removed", "DELETE /api/v1/secrets/REMOTE_WAKE_KEY"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("204 without --def points at the definition", func(t *testing.T) {
		srv := newRetireStub(t, http.StatusNoContent, "")
		out, err := runRootIn(t, "", "remote", "retire", "rmt_x", "--yes", "--server", srv.ts.URL)
		if err != nil {
			t.Fatalf("retire: %v\n%s", err, out)
		}
		for _, want := range []string{"wake_key", "is not removed", "DELETE /api/v1/secrets/<key>"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("unknown --def sends nothing", func(t *testing.T) {
		srv := newRetireStub(t, http.StatusNoContent, "")
		if _, err := runRootIn(t, "", "remote", "retire", "rmt_x", "--yes", "--def", "nope", "--server", srv.ts.URL); err == nil {
			t.Fatal("an unknown definition must fail")
		}
		if n := len(srv.requests()); n != 0 {
			t.Errorf("want no request, got %d", n)
		}
	})
	for _, c := range []struct {
		code int
		want string
		as   any
	}{
		{http.StatusNotFound, "not found or already retired", new(*hosted.RemoteNotFoundError)},
		{http.StatusForbidden, "only the remote's creator or an admin of its project may retire", new(*hosted.RemoteForbiddenError)},
	} {
		t.Run(fmt.Sprintf("%d fails", c.code), func(t *testing.T) {
			srv := newRetireStub(t, c.code, `{"error":"x"}`)
			out, err := runRootIn(t, "", "remote", "retire", "rmt_x", "--yes", "--server", srv.ts.URL)
			if err == nil {
				t.Fatalf("a %d must exit non-zero:\n%s", c.code, out)
			}
			if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "rmt_x") || !errors.As(err, c.as) {
				t.Errorf("error = %v, want %q naming rmt_x as %T", err, c.want, c.as)
			}
		})
	}
}

// TestNoImplicitRetireOnServerPaths runs every other server-talking remote
// command and shows none of them sends a DELETE.
func TestNoImplicitRetireOnServerPaths(t *testing.T) {
	remoteWithTriggers(t)
	srv := newStubServer(t, "")
	for _, args := range [][]string{
		{"register", stubRemoteID, "--def", "edge"},
		{"fire", stubRemoteID, "nightly"},
		{"pause", stubRemoteID, "nightly"},
		{"resume", stubRemoteID, "nightly"},
	} {
		if out, err := runRoot(t, append(append([]string{"remote", "trigger"}, args...), "--server", srv.ts.URL)...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	rs := srv.requests()
	if len(rs) == 0 {
		t.Fatal("the stub saw no request")
	}
	for _, r := range rs {
		if r.Method == http.MethodDelete {
			t.Errorf("a trigger command sent %s %s", r.Method, r.Path)
		}
	}
}

// retireOwner and retireClient are the only places a remote may be retired.
const (
	retireCommandFile = "internal/cli/cmd_remote_retire.go"
	retireClientFile  = "internal/hosted/client_remotes.go"
)

// TestRetireIsOnlyExplicit scans every non-test Go file of the module and fails
// if a RetireRemote call appears outside the retire command, or if a function
// pairs http.MethodDelete with remoteRoute anywhere but RetireRemote. That keeps
// any stop, teardown or lifecycle path (host.stop included, once it merges) from
// retiring a remote.
func TestRetireIsOnlyExplicit(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	exempt := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Errorf("parse %s: %v", rel, perr)
			return nil
		}
		for _, decl := range f.Decls {
			var calls, hasDelete, hasRoute bool
			ast.Inspect(decl, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if calleeName(x.Fun) == "RetireRemote" {
						calls = true
					}
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "http" && x.Sel.Name == "MethodDelete" {
						hasDelete = true
					}
				case *ast.Ident:
					if x.Name == "remoteRoute" {
						hasRoute = true
					}
				}
				return true
			})
			if calls && rel != retireCommandFile {
				t.Errorf("%s calls RetireRemote; only %s may", rel, retireCommandFile)
			}
			if hasDelete && hasRoute {
				fd, ok := decl.(*ast.FuncDecl)
				if ok && fd.Name.Name == "RetireRemote" && rel == retireClientFile {
					exempt++
					continue
				}
				t.Errorf("%s pairs http.MethodDelete with remoteRoute outside RetireRemote in %s", rel, retireClientFile)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exempt != 1 {
		t.Errorf("found %d exempt RetireRemote declarations in %s, want 1 — the scan has nothing to check against", exempt, retireClientFile)
	}
}

func calleeName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
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
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// protectedRemote is a real remote that no test may retire.
const protectedRemote = "rmt_40abf64d121c"

// TestLiveRetire registers a throwaway remote on a real satelle-server, retires
// it, and retires it again for the not-found error. It is skipped unless
// SATELLE_LIVE_SERVER, SATELLE_LIVE_PROJECT and SATELLE_TOKEN are set.
func TestLiveRetire(t *testing.T) {
	server, project, token := os.Getenv("SATELLE_LIVE_SERVER"), os.Getenv("SATELLE_LIVE_PROJECT"), os.Getenv(hosted.SessionTokenEnv)
	if server == "" || project == "" || token == "" {
		t.Skip("set SATELLE_LIVE_SERVER, SATELLE_LIVE_PROJECT and SATELLE_TOKEN to run against a real satelle-server")
	}
	retireRepo(t)
	t.Setenv(hosted.SessionTokenEnv, token) // retireRepo installs the stub's
	server = strings.TrimRight(server, "/")

	// The wake target 404s and the secret key is never set, so no wake is ever sent.
	payload, _ := json.Marshal(map[string]string{
		"name":            fmt.Sprintf("retire-test-%d", time.Now().UnixNano()),
		"wake_url":        "https://satelle.dev/interop-test-sink",
		"wake_secret_key": "SATELLE_INTEROP_TEST_UNSET",
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server+"/api/v1/projects/"+project+"/remotes", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("register a throwaway remote: HTTP %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("the registration answered no remote id: %s", body)
	}
	if created.ID == protectedRemote {
		t.Fatalf("the registration returned the protected remote %s; refusing to retire it", protectedRemote)
	}
	id := created.ID
	// If the test stops early, do not leave the throwaway remote registered.
	retired := false
	t.Cleanup(func() {
		if !retired {
			_ = hosted.NewClient(server, hosted.DefaultStore(), nil).RetireRemote(context.Background(), id)
		}
	})

	out, err := runRootIn(t, "", "remote", "retire", id, "--yes", "--server", server)
	if err != nil {
		t.Fatalf("retire: %v\n%s", err, out)
	}
	retired = true
	if !strings.Contains(out, "retired") {
		t.Errorf("retire output:\n%s", out)
	}
	_, err = runRootIn(t, "", "remote", "retire", id, "--yes", "--server", server)
	var nf *hosted.RemoteNotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("a second retire must be not-found, got %v", err)
	}
}
