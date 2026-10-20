package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// devCall is one request the stub satelle.dev received.
type devCall struct {
	Method, Path, Auth string
	Body               string
}

// devStub is a satelle.dev for the owner's calls: secrets, grants, keys, identity
// and runs. answer, when set, may override the response of a request (non-zero
// status).
type devStub struct {
	t      *testing.T
	ts     *httptest.Server
	mu     sync.Mutex
	calls  []devCall
	answer func(c devCall) (status int, body string)
	// secrets is the list the GET answers, by listing path.
	secrets map[string]string
	runID   string
}

func newDevStub(t *testing.T) *devStub {
	t.Helper()
	d := &devStub{t: t, secrets: map[string]string{}, runID: "trg_run1"}
	d.ts = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.ts.Close)
	return d
}

func (d *devStub) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c := devCall{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(raw)}
	d.mu.Lock()
	d.calls = append(d.calls, c)
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if d.answer != nil {
		if status, body := d.answer(c); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && d.secrets[r.URL.Path] != "":
		_, _ = io.WriteString(w, d.secrets[r.URL.Path])
	case r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `[]`)
	case strings.HasSuffix(r.URL.Path, "/key"):
		_, _ = io.WriteString(w, `{"fingerprint":"SHA256:fp","set_at":"2026-10-20T00:00:00Z"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"run_id":"`+d.runID+`","expires_at":"2030-01-01T00:00:00Z"}`)
	case strings.HasSuffix(r.URL.Path, "/refresh"):
		_, _ = io.WriteString(w, `{"run_id":"`+d.runID+`","expires_at":"2030-01-01T00:00:00Z"}`)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/identity"):
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (d *devStub) all() []devCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]devCall(nil), d.calls...)
}

// find returns the calls with the method and path.
func (d *devStub) find(method, path string) []devCall {
	var out []devCall
	for _, c := range d.all() {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (d *devStub) body(c devCall) map[string]string {
	var m map[string]string
	_ = json.Unmarshal([]byte(c.Body), &m)
	return m
}

const secretWS = "0f7b1c55-3b8e-4c8a-9d7a-5d2d3c1a9b10"

// secretRepo is a repo, signed in to the stub, with a remote "edge" whose
// definition is the scaffold plus extra top-level keys and extra tables.
func secretRepo(t *testing.T, d *devStub, topLevel, tables string) string {
	t.Helper()
	repo := tempRepo(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	seedCred(t, d.ts.URL)
	t.Chdir(repo)
	if _, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(remoteDir(repo, "edge"), "remote.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), `wake_key = "REMOTE_WAKE_KEY"`, `wake_key = "REMOTE_WAKE_KEY"`+"\n"+topLevel, 1)
	if err := os.WriteFile(p, []byte(s+"\n"+tables+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func eventuallyCli(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeDotEnvFile(t *testing.T, repo, body string) {
	t.Helper()
	p := filepath.Join(repo, ".satelle", "config", ".env")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
