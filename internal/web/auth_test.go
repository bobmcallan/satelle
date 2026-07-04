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

	"github.com/bobmcallan/satelle/internal/hosted"
)

// resetAuthState clears the package-level sign-in state between tests.
func resetAuthState(t *testing.T) {
	t.Helper()
	pendingMu.Lock()
	pending = map[string]*pendingFlow{}
	pendingMu.Unlock()
	clearPrincipalCache()
	setHostedServer("")
	t.Cleanup(func() {
		pendingMu.Lock()
		pending = map[string]*pendingFlow{}
		pendingMu.Unlock()
		clearPrincipalCache()
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
