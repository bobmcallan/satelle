// Package devtest is a stand-in for the routes satelle.dev serves to a remote
// node, for tests: GET /api/v1/remote-nodes/{id}/definition,
// POST .../secrets/{key} and POST .../end, all keyed by the node's id and taking
// the node token as a bearer (satelle-server sty_c9013137). A call under any other
// id is a 404; a bad token, or one an end call or Revoke has ended, is a 401. It
// records every request. It is for tests only: nothing in the product imports it.
package devtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// SecretValue is a value for a test to grant: one that must appear nowhere but the
// agent's environment.
const SecretValue = "sk-ant-oat01-NEVER-WRITTEN-ANYWHERE"

// Call is one request the fake received.
type Call struct {
	Method, Path, Auth string
	// Status is what the fake answered.
	Status int
}

// Node is the fake. Create it with NewNode.
type Node struct {
	// URL is the base URL to hand to the node as its satelle.dev.
	URL string
	// Secrets are the values the node is granted, by key. A key not in it is
	// refused: not declared, or not granted.
	Secrets map[string]string
	// ETag, when set, replaces the definition's real ETag, and OmitVersion leaves
	// the version header off: a server that does not keep its contract.
	ETag        string
	OmitVersion bool

	srv     *httptest.Server
	id      string
	bundle  []byte
	version string

	mu      sync.Mutex
	token   string
	revoked bool
	calls   []Call
}

// NewNode starts a fake for node id whose token is token, serving bundle as the
// stored definition at version, for the life of t.
func NewNode(t testing.TB, id, token string, bundle []byte, version string) *Node {
	t.Helper()
	n := &Node{id: id, token: token, bundle: bundle, version: version, Secrets: map[string]string{}}
	n.srv = httptest.NewServer(http.HandlerFunc(n.serve))
	n.URL = n.srv.URL
	t.Cleanup(n.srv.Close)
	return n
}

// Client is an HTTP client for the fake's own server.
func (n *Node) Client() *http.Client { return n.srv.Client() }

// SetToken replaces the token the fake accepts, for a test whose token is minted
// after the fake starts.
func (n *Node) SetToken(token string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.token = token
}

// Revoke ends the node's token, as the server does when it ends the node or an
// admin revokes it: every later call is a 401.
func (n *Node) Revoke() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.revoked = true
}

// Calls is every request received, in order.
func (n *Node) Calls() []Call {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Call(nil), n.calls...)
}

// Count is how many requests ended with tail (for example "/end" or
// "/secrets/KEY") under the node's own id.
func (n *Node) Count(method, tail string) int {
	count := 0
	for _, c := range n.Calls() {
		if c.Method == method && c.Path == "/api/v1/remote-nodes/"+n.id+tail {
			count++
		}
	}
	return count
}

func (n *Node) serve(w http.ResponseWriter, r *http.Request) {
	status := n.answer(w, r)
	n.mu.Lock()
	n.calls = append(n.calls, Call{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Status: status})
	n.mu.Unlock()
}

func (n *Node) answer(w http.ResponseWriter, r *http.Request) int {
	prefix := "/api/v1/remote-nodes/" + n.id + "/"
	tail, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		return reply(w, http.StatusNotFound, `{"error":"not_found"}`)
	}
	n.mu.Lock()
	good := !n.revoked && r.Header.Get("Authorization") == "Bearer "+n.token
	n.mu.Unlock()
	if !good {
		return reply(w, http.StatusUnauthorized, `{"error":"invalid_token"}`)
	}
	switch {
	case r.Method == http.MethodGet && tail == "definition":
		sum := sha256.Sum256(n.bundle)
		etag := hex.EncodeToString(sum[:])
		if n.ETag != "" {
			etag = n.ETag
		}
		w.Header().Set("ETag", `"`+etag+`"`)
		if !n.OmitVersion {
			w.Header().Set("X-Satelle-Definition-Version", n.version)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(n.bundle)
		return http.StatusOK
	case r.Method == http.MethodPost && strings.HasPrefix(tail, "secrets/"):
		key := strings.TrimPrefix(tail, "secrets/")
		value, granted := n.Secrets[key]
		if !granted {
			return reply(w, http.StatusForbidden, `{"error":"secret_refused","reason":"the key is not declared by this definition or not granted to it"}`)
		}
		raw, _ := json.Marshal(map[string]string{"value": value})
		return reply(w, http.StatusOK, string(raw))
	case r.Method == http.MethodPost && tail == "end":
		n.Revoke()
		w.WriteHeader(http.StatusNoContent)
		return http.StatusNoContent
	}
	return reply(w, http.StatusNotFound, `{"error":"not_found"}`)
}

func reply(w http.ResponseWriter, status int, body string) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
	return status
}
