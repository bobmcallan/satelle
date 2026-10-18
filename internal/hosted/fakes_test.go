package hosted

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// loopbackOnly wraps base so any request to a non-loopback host fails the test
// before it leaves the machine. It reuses the production isLoopbackURL guard,
// so no login, refresh or logout test can reach a real identity provider.
type loopbackOnly struct {
	t    *testing.T
	base http.RoundTripper
}

func (l loopbackOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isLoopbackURL(req.URL.String()) {
		l.t.Errorf("test request left the loopback: %s", req.URL)
		return nil, fmt.Errorf("non-loopback request refused: %s", req.URL)
	}
	if l.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return l.base.RoundTrip(req)
}

// guardedClient is an http.Client that only reaches loopback hosts.
func guardedClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: loopbackOnly{t: t}}
}

// requireLoopback fails t unless every url is loopback.
func requireLoopback(t *testing.T, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if !isLoopbackURL(u) {
			t.Fatalf("fake URL %q is not loopback", u)
		}
	}
}

// otherHost rewrites a loopback server URL onto a different hostname that still
// resolves to loopback, so a test can put the authorization server on a
// different host from the resource server.
func otherHost(t *testing.T, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()
	requireLoopback(t, u.String())
	return u.String()
}

// fakeProvider is a token endpoint that records every request and rotates the
// pair on each refresh (access-N / refresh-N, N counting from 2).
type fakeProvider struct {
	ts *httptest.Server

	mu    sync.Mutex
	forms []url.Values
	deny  bool // answer invalid_grant instead of rotating
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	p := &fakeProvider{}
	p.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		p.forms = append(p.forms, r.PostForm)
		n, deny := len(p.forms), p.deny
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if deny {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fmt.Sprintf("access-%d", n+1),
			"refresh_token": fmt.Sprintf("refresh-%d", n+1),
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(p.ts.Close)
	requireLoopback(t, p.ts.URL)
	return p
}

// tokenURL is the token endpoint to store on a credential.
func (p *fakeProvider) tokenURL() string { return p.ts.URL + "/token" }

// credential is a login-shaped credential whose refresh targets this provider.
func (p *fakeProvider) credential(server string) Credential {
	return Credential{
		ServerURL: server, AccessToken: "access-1", RefreshToken: "refresh-1",
		TokenEndpoint: p.tokenURL(), ClientID: "client-abc", Resource: "https://api.example/",
	}
}

func (p *fakeProvider) refreshes() []url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]url.Values(nil), p.forms...)
}

// wantRefresh fails t unless the i-th refresh carried token, the credential's
// client id and the resource.
func (p *fakeProvider) wantRefresh(t *testing.T, i int, token string) {
	t.Helper()
	got := p.refreshes()
	if len(got) <= i {
		t.Fatalf("provider saw %d refresh(es), want index %d", len(got), i)
	}
	f := got[i]
	if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != token {
		t.Errorf("refresh %d = %v, want refresh_token %q", i, f, token)
	}
	if f.Get("client_id") != "client-abc" || f.Get("resource") != "https://api.example/" {
		t.Errorf("refresh %d lost client id or resource: %v", i, f)
	}
}

// noPath fails the test if the server the CLI signs in to ever receives path.
func noPath(t *testing.T, mux *http.ServeMux, patterns ...string) {
	t.Helper()
	for _, p := range patterns {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("satelle server must not receive %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		})
	}
}
