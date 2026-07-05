package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// TestGlobalLoginReflectedOnRepoWithoutHostedConfig proves the point of the epic
// (sty_53ccf845): with the hosted server bound GLOBALLY, one sign-in shows the
// same identity on a project whose own satelle.toml has NO [hosted] server — the
// per-project signed-in-here/signed-out-there split is gone.
func TestGlobalLoginReflectedOnRepoWithoutHostedConfig(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // credential store
	t.Setenv("SATELLE_HOME", t.TempDir())    // global config

	const server = "https://global.example"
	if err := config.SaveGlobalHostedServer(server); err != nil {
		t.Fatal(err)
	}

	// web.New binds the topbar server global-first; a repo Config with no [hosted]
	// still resolves to the global server.
	setHostedServer(config.ResolveHostedServer(config.Config{}))
	if hostedServer != server {
		t.Fatalf("web did not bind to the global hosted server: got %q", hostedServer)
	}

	// The credential from one global login makes the topbar signed-in here even
	// though this repo carries no hosted server of its own.
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: server, AccessToken: "a", RefreshToken: "r",
		DisplayName: "Dev", Email: "dev@x.io",
	}); err != nil {
		t.Fatal(err)
	}
	u := resolveUser()
	if u == nil || u.Email != "dev@x.io" {
		t.Fatalf("global login not reflected on a repo without [hosted]: %+v", u)
	}
}

// resetAuthState clears the package-level sign-in state between tests.
func resetAuthState(t *testing.T) {
	t.Helper()
	pendingMu.Lock()
	pending = map[string]*pendingFlow{}
	pendingMu.Unlock()
	resetWarmState()
	setHostedServer("")
	t.Cleanup(func() {
		pendingMu.Lock()
		pending = map[string]*pendingFlow{}
		pendingMu.Unlock()
		resetWarmState()
		setHostedServer("")
	})
}

// tokenStub is an OAuth token endpoint stub counting its hits.
func tokenStub(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			mu.Lock()
			calls++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "acc", "refresh_token": "ref",
				"token_type": "Bearer", "expires_in": 3600, "scope": "satelle",
			})
			return
		}
		if r.URL.Path == "/api/v1/me" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": "u1", "email": "dev@satelle.dev", "display_name": "Dev User", "role": "member",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

func TestOAuthCallbackHappyPathSharesStore(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts, calls := tokenStub(t)
	setHostedServer(ts.URL)

	// /oauth/login registers a flow and 302s to authorize; pull the state back.
	rec := httptest.NewRecorder()
	oauthLogin(rec, httptest.NewRequest(http.MethodGet, "/oauth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login status = %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("no state in authorize redirect")
	}

	// The callback exchanges the code and persists to the shared XDG store.
	rec2 := httptest.NewRecorder()
	oauthCallback(rec2, httptest.NewRequest(http.MethodGet, "/oauth/callback?code=c&state="+state, nil))
	if rec2.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	cred, err := (hosted.FileStore{}).Load(ts.URL)
	if err != nil {
		t.Fatalf("credential not persisted to shared store: %v", err)
	}
	if cred.AccessToken != "acc" || cred.RefreshToken != "ref" {
		t.Fatalf("tokens = %+v", cred)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 token call, got %d", *calls)
	}
	// The callback stamped the principal into the credential (AC2 web half).
	if cred.DisplayName != "Dev User" || cred.Email != "dev@satelle.dev" {
		t.Fatalf("identity not persisted at sign-in: %+v", cred)
	}
	// And resolveUser now renders it from the LOCAL file — with the stub stopped.
	ts.Close()
	if u := resolveUser(); u == nil || u.Email != "dev@satelle.dev" {
		t.Fatalf("resolveUser should read identity locally, got %+v", u)
	}
}

// AC1: a render must not call the network. With identity stored, resolveUser
// returns it even when the hosted server is unreachable (a network call would
// hang/fail).
func TestResolveUserNoNetworkWhenIdentityStored(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	setHostedServer("http://127.0.0.1:1") // unreachable (a fetch would fail/block)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "http://127.0.0.1:1", AccessToken: "a", RefreshToken: "r",
		DisplayName: "Local Only", Email: "local@x.io",
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	u := resolveUser()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("resolveUser blocked %v — it must not call the network", elapsed)
	}
	if u == nil || u.Name != "Local Only" || u.Email != "local@x.io" {
		t.Fatalf("identity not resolved locally: %+v", u)
	}
}

// AC3: a legacy credential (tokens, no identity) does not block; the background
// warm writes identity for a later render.
func TestResolveUserLegacyWarmsInBackground(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts, _ := tokenStub(t) // serves /api/v1/me
	setHostedServer(ts.URL)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "acc", RefreshToken: "ref", // no identity
	}); err != nil {
		t.Fatal(err)
	}
	// First render: signed-out (nil), non-blocking, kicks the warm.
	if u := resolveUser(); u != nil {
		t.Fatalf("legacy cred should render signed-out first, got %+v", u)
	}
	// The warm writes identity into the credential; poll until it lands.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := (hosted.FileStore{}).Load(ts.URL); c.Email != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, _ := (hosted.FileStore{}).Load(ts.URL)
	if c.Email != "dev@satelle.dev" {
		t.Fatalf("background warm did not persist identity: %+v", c)
	}
	resetWarmState()
	if u := resolveUser(); u == nil || u.Email != "dev@satelle.dev" {
		t.Fatalf("second render should show warmed identity, got %+v", u)
	}
}

func TestOAuthCallbackStateMismatchNoExchange(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts, calls := tokenStub(t)
	setHostedServer(ts.URL)

	// No pending flow registered → unknown state must abort BEFORE the token call.
	rec := httptest.NewRecorder()
	oauthCallback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?code=c&state=bogus", nil))
	if rec.Code == http.StatusFound {
		t.Fatalf("state mismatch must not succeed, got %d", rec.Code)
	}
	if *calls != 0 {
		t.Fatalf("token endpoint must not be called on state mismatch, got %d", *calls)
	}
	if _, err := (hosted.FileStore{}).Load(ts.URL); err == nil {
		t.Fatal("no credential should be stored on state mismatch")
	}
}

func TestOAuthLogoutClearsSharedStore(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const server = "https://logout.example"
	setHostedServer(server)
	// Seed a credential in the shared store, then sign out.
	if err := (hosted.FileStore{}).Save(hosted.Credential{ServerURL: server, AccessToken: "acc", RefreshToken: "ref"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	oauthLogout(rec, httptest.NewRequest(http.MethodPost, "/oauth/logout", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("logout status = %d", rec.Code)
	}
	if _, err := (hosted.FileStore{}).Load(server); err == nil {
		t.Fatal("logout did not clear the credential from the shared store")
	}
	// The topbar now resolves to signed-out (no network, credential gone).
	if u := resolveUser(); u != nil {
		t.Fatalf("after logout the topbar should be signed out, got %+v", u)
	}
}

func TestTopbarRendersSignedOut(t *testing.T) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "topbar", topBar{Uptime: "up 1m", User: nil}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="signin"`) || !strings.Contains(out, `href="oauth/login"`) {
		t.Fatalf("signed-out topbar missing Sign in control:\n%s", out)
	}
	if strings.Contains(out, "account-menu") {
		t.Fatalf("signed-out topbar should not render the account menu:\n%s", out)
	}
}

func TestTopbarRendersSignedIn(t *testing.T) {
	var buf bytes.Buffer
	u := &topBarUser{Name: "Dev User", Email: "dev@satelle.dev", Initial: "D"}
	if err := tmpl.ExecuteTemplate(&buf, "topbar", topBar{Uptime: "up 1m", User: u}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`class="avatar"`, ">D<", "Dev User", "dev@satelle.dev", `action="oauth/logout"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("signed-in topbar missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `class="signin"`) {
		t.Fatalf("signed-in topbar should not render Sign in:\n%s", out)
	}
}

func TestResolveUserUnconfiguredAndNoCredential(t *testing.T) {
	resetAuthState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Unconfigured → nil, no network.
	if u := resolveUser(); u != nil {
		t.Fatalf("unconfigured should be signed out, got %+v", u)
	}
	// Configured but no stored credential → nil (local file read only).
	setHostedServer("https://hosted.example")
	if u := resolveUser(); u != nil {
		t.Fatalf("no credential should be signed out, got %+v", u)
	}
}

func TestOAuthLoginUnconfiguredIsFriendly(t *testing.T) {
	resetAuthState(t) // hostedServer == ""
	rec := httptest.NewRecorder()
	oauthLogin(rec, httptest.NewRequest(http.MethodGet, "/oauth/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unconfigured login status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hosted server") {
		t.Fatalf("expected a friendly not-configured page:\n%s", rec.Body.String())
	}
}
