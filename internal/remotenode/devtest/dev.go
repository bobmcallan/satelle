// Package devtest is a stand-in for satelle.dev's signed-read routes, for tests. It
// verifies a request's v1 signature the way the real server does (the same
// message, over the public key of a fixed test seed), answers a read with a fixed
// value and records every request. It is for tests only: nothing in the product
// imports it.
package devtest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/remotesign"
)

const (
	// SeedB64 is the private key the stub verifies against, as a remote's host
	// would hold it.
	SeedB64 = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	// SecretValue is what an allowed read returns.
	SecretValue = "sk-ant-oat01-NEVER-WRITTEN-ANYWHERE"
)

// Call is one request the stub received.
type Call struct {
	Method, Path, Auth string
	Header             http.Header
	Body               map[string]string
	// Verified says the signature verifies over Message under the test key.
	Verified bool
	Message  string
}

// Dev is the stub. Mount Handler on a server, or use URL and Client from New.
type Dev struct {
	URL string
	// Refuse, when set, may refuse a request: a non-zero status is answered with
	// the body.
	Refuse func(c Call) (status int, body string)

	srv   *httptest.Server
	mu    sync.Mutex
	calls []Call
	pub   ed25519.PublicKey
}

// New starts a stub server for the life of t.
func New(t testing.TB) *Dev {
	t.Helper()
	d := newDev()
	d.srv = httptest.NewServer(d.Handler())
	d.URL = d.srv.URL
	t.Cleanup(d.srv.Close)
	return d
}

func newDev() *Dev {
	seed, _ := base64.StdEncoding.DecodeString(SeedB64)
	return &Dev{pub: ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)}
}

// Client is an HTTP client for the stub's own server.
func (d *Dev) Client() *http.Client { return d.srv.Client() }

// Handler serves /api/v1/remotes/{id}/runs/{trigger}[/secrets/{key} | /refresh].
func (d *Dev) Handler() http.Handler { return http.HandlerFunc(d.serve) }

func (d *Dev) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c := Call{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Header: r.Header.Clone()}
	_ = json.Unmarshal(raw, &c.Body)
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/remotes/"), "/")
	req := remotesign.Request{Method: r.Method, Path: r.URL.Path}
	if len(parts) >= 3 {
		req.TriggerID = parts[2]
	}
	if len(parts) >= 5 && parts[3] == "secrets" {
		req.Key, req.Scope, req.Workspace = parts[4], c.Body["scope"], c.Body["workspace"]
	}
	c.Message = req.Message(r.Header.Get(remotesign.HeaderNonce), r.Header.Get(remotesign.HeaderTimestamp))
	if sig, err := base64.StdEncoding.DecodeString(r.Header.Get(remotesign.HeaderSignature)); err == nil {
		c.Verified = ed25519.Verify(d.pub, []byte(c.Message), sig)
	}
	d.mu.Lock()
	d.calls = append(d.calls, c)
	d.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if d.Refuse != nil {
		if code, body := d.Refuse(c); code != 0 {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, body)
			return
		}
	}
	switch {
	case strings.Contains(r.URL.Path, "/secrets/"):
		_, _ = io.WriteString(w, `{"value":"`+SecretValue+`"}`)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		_, _ = io.WriteString(w, `{"run_id":"x","expires_at":"2030-01-01T00:00:00Z"}`)
	}
}

// Calls is every request received, in order.
func (d *Dev) Calls() []Call {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Call(nil), d.calls...)
}
