package hosted

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// authServer is a stub OAuth 2.1 + PKCE authorization server for the flow tests.
type authServer struct {
	mu           sync.Mutex
	challenge    string // captured from /oauth/authorize
	stateEcho    string // state to echo back (default: the real one)
	forceState   string // if set, echo THIS wrong state instead
	forceError   string // if set, redirect with error= and no code
	tokenCalls   int
	gotVerifier  string
	gotRedirect  string
	gotGrant     string
	gotClientID  string
	authRedirect string // captured redirect_uri from authorize
}

func newAuthServer() (*authServer, *httptest.Server) {
	a := &authServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a.mu.Lock()
		a.challenge = q.Get("code_challenge")
		a.authRedirect = q.Get("redirect_uri")
		method := q.Get("code_challenge_method")
		a.mu.Unlock()
		if method != "S256" {
			http.Error(w, "only S256", http.StatusBadRequest)
			return
		}
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		vals := url.Values{}
		if a.forceError != "" {
			vals.Set("error", a.forceError)
			vals.Set("state", state)
			http.Redirect(w, r, redirect+"?"+vals.Encode(), http.StatusFound)
			return
		}
		echo := state
		if a.forceState != "" {
			echo = a.forceState
		}
		vals.Set("code", "auth-code-123")
		vals.Set("state", echo)
		http.Redirect(w, r, redirect+"?"+vals.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a.mu.Lock()
		a.tokenCalls++
		a.gotVerifier = r.Form.Get("code_verifier")
		a.gotRedirect = r.Form.Get("redirect_uri")
		a.gotGrant = r.Form.Get("grant_type")
		a.gotClientID = r.Form.Get("client_id")
		a.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "satelle",
		})
	})
	return a, httptest.NewServer(mux)
}

// browserGET returns an OpenBrowser that simply GETs the authorize URL, letting
// the stub's 302 flow the code onto the loopback listener.
func browserGET(t *testing.T) func(string) error {
	return func(authURL string) error {
		go func() {
			resp, err := http.Get(authURL)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoginHappyPath(t *testing.T) {
	a, ts := newAuthServer()
	defer ts.Close()

	cred, err := Login(context.Background(), ts.Client(), ts.URL, LoginOptions{
		OpenBrowser: browserGET(t),
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if cred.AccessToken != "access-1" || cred.RefreshToken != "refresh-1" {
		t.Fatalf("tokens not captured: %+v", cred)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokenCalls != 1 {
		t.Fatalf("expected exactly 1 token call, got %d", a.tokenCalls)
	}
	if a.gotGrant != "authorization_code" {
		t.Errorf("grant_type = %q", a.gotGrant)
	}
	if a.gotClientID != ClientID {
		t.Errorf("client_id = %q, want %q", a.gotClientID, ClientID)
	}
	// redirect_uri sent to /token must be byte-identical to the one authorize saw.
	if a.gotRedirect != a.authRedirect {
		t.Errorf("redirect_uri mismatch: token=%q authorize=%q", a.gotRedirect, a.authRedirect)
	}
	if !strings.HasPrefix(a.gotRedirect, "http://127.0.0.1:") {
		t.Errorf("redirect_uri not loopback: %q", a.gotRedirect)
	}
	// The verifier must be 43-128 chars and S256-hash to the sent challenge.
	if n := len(a.gotVerifier); n < 43 || n > 128 {
		t.Errorf("verifier length %d out of range", n)
	}
	sum := sha256.Sum256([]byte(a.gotVerifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != a.challenge {
		t.Errorf("S256(verifier) %q != challenge %q", got, a.challenge)
	}
}

func TestLoginStateMismatchAbortsBeforeExchange(t *testing.T) {
	a, ts := newAuthServer()
	defer ts.Close()
	a.forceState = "wrong-state"

	_, err := Login(context.Background(), ts.Client(), ts.URL, LoginOptions{
		OpenBrowser: browserGET(t),
		Timeout:     3 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch error, got %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokenCalls != 0 {
		t.Fatalf("token endpoint must NOT be called on state mismatch, got %d calls", a.tokenCalls)
	}
}

func TestLoginErrorParamNoExchange(t *testing.T) {
	a, ts := newAuthServer()
	defer ts.Close()
	a.forceError = "access_denied"

	_, err := Login(context.Background(), ts.Client(), ts.URL, LoginOptions{
		OpenBrowser: browserGET(t),
		Timeout:     3 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("expected authorization error, got %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokenCalls != 0 {
		t.Fatalf("token endpoint must NOT be called on error redirect, got %d", a.tokenCalls)
	}
}

func TestNewPKCEShape(t *testing.T) {
	pk, err := newPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pk.verifier); n < 43 || n > 128 {
		t.Errorf("verifier length %d out of RFC range", n)
	}
	if _, err := base64.RawURLEncoding.DecodeString(pk.verifier); err != nil {
		t.Errorf("verifier not base64url: %v", err)
	}
	sum := sha256.Sum256([]byte(pk.verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); want != pk.challenge {
		t.Errorf("challenge != S256(verifier)")
	}
}
