package hosted

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientID is the public client identifier for the satelle CLI. It is a
// constant (RFC 8252 native app, no client secret); the hosted server
// special-cases the loopback redirect so no per-run dynamic registration is
// needed.
const ClientID = "satelle-cli"

// defaultLoginTimeout bounds the wait for the browser to land the callback.
const defaultLoginTimeout = 3 * time.Minute

// tokenResponse is the /oauth/token success body (both grants).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// oauthError is the /oauth/token error body ({"error","error_description"}).
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// LoginOptions tunes the login flow; the zero value is valid.
type LoginOptions struct {
	// OpenBrowser opens authURL; nil means don't try (only print it). Injected
	// by tests to drive the loopback flow without a real browser.
	OpenBrowser func(authURL string) error
	// Timeout overrides defaultLoginTimeout.
	Timeout time.Duration
	// Out receives the "open this URL" prompt; nil discards it.
	Out io.Writer
}

// pkce holds one flow's verifier + S256 challenge.
type pkce struct {
	verifier  string
	challenge string
}

// newPKCE generates an RFC 7636 S256 verifier (32 random bytes → 43-char
// base64url, within the 43-128 range) and its challenge.
func newPKCE() (pkce, error) {
	v, err := randomURLToken(32)
	if err != nil {
		return pkce{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return pkce{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hosted: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Login runs the loopback authorization-code + PKCE flow end to end against
// server and returns the resulting Credential (NOT persisted — the caller
// stores it). httpClient is the client used for the token exchange.
func Login(ctx context.Context, httpClient *http.Client, server string, opts LoginOptions) (Credential, error) {
	server = normalizeServerURL(server)
	if server == "" {
		return Credential{}, fmt.Errorf("hosted: server URL not set (pass --server or set [hosted] server in satelle.toml)")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultLoginTimeout
	}

	// 1. Loopback listener on an ephemeral port. Build the redirect URI ONCE and
	//    reuse the exact string for BOTH authorize and the token exchange — the
	//    server matches it byte-for-byte, so rebuilding it would fail.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Credential{}, fmt.Errorf("hosted: open loopback listener: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// 2. PKCE (S256) + CSRF state.
	pk, err := newPKCE()
	if err != nil {
		return Credential{}, err
	}
	state, err := randomURLToken(16)
	if err != nil {
		return Credential{}, err
	}

	// 3. One-shot callback handler. A state mismatch or an error= param fails the
	//    flow WITHOUT calling the token endpoint.
	type cbResult struct {
		code string
		err  error
	}
	resultCh := make(chan cbResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			writeCallbackPage(w, "Authentication failed: "+q.Get("error"))
			resultCh <- cbResult{err: fmt.Errorf("hosted: authorization error: %s", q.Get("error"))}
		case q.Get("state") != state:
			writeCallbackPage(w, "Authentication failed: state mismatch.")
			resultCh <- cbResult{err: fmt.Errorf("hosted: state mismatch (possible CSRF) — aborted before token exchange")}
		case q.Get("code") == "":
			writeCallbackPage(w, "Authentication failed: no authorization code.")
			resultCh <- cbResult{err: fmt.Errorf("hosted: no authorization code in callback")}
		default:
			writeCallbackPage(w, "Authentication complete — you may close this tab.")
			resultCh <- cbResult{code: q.Get("code")}
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 4. Send the operator to the authorize URL (always print it; open if we can).
	authURL := buildAuthorizeURL(server, redirectURI, pk.challenge, state)
	if opts.Out != nil {
		fmt.Fprintf(opts.Out, "Open this URL to authenticate:\n\n  %s\n\n", authURL)
	}
	if opts.OpenBrowser != nil {
		if err := opts.OpenBrowser(authURL); err != nil && opts.Out != nil {
			fmt.Fprintf(opts.Out, "(could not open a browser automatically: %v)\n", err)
		}
	}

	// 5. Await the code (or a timeout / cancel).
	var code string
	select {
	case res := <-resultCh:
		if res.err != nil {
			return Credential{}, res.err
		}
		code = res.code
	case <-time.After(timeout):
		return Credential{}, fmt.Errorf("hosted: timed out after %s waiting for browser approval", timeout)
	case <-ctx.Done():
		return Credential{}, ctx.Err()
	}

	// 6. Exchange the code — redirectURI is the SAME string sent to authorize.
	tok, err := exchangeCode(ctx, httpClient, server, code, redirectURI, pk.verifier)
	if err != nil {
		return Credential{}, err
	}
	return credentialFromToken(server, tok), nil
}

// buildAuthorizeURL builds the /oauth/authorize URL. Method is always S256.
func buildAuthorizeURL(server, redirectURI, challenge, state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return server + "/oauth/authorize?" + q.Encode()
}

// exchangeCode performs the authorization_code grant.
func exchangeCode(ctx context.Context, httpClient *http.Client, server, code, redirectURI, verifier string) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", ClientID)
	form.Set("code_verifier", verifier)
	return postToken(ctx, httpClient, server, form)
}

// refreshGrant performs the refresh_token grant, returning a rotated pair.
func refreshGrant(ctx context.Context, httpClient *http.Client, server, refreshToken string) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", ClientID)
	return postToken(ctx, httpClient, server, form)
}

// postToken POSTs a form to /oauth/token and decodes the response, turning a
// non-200 into a typed *oauthError.
func postToken(ctx context.Context, httpClient *http.Client, server string, form url.Values) (tokenResponse, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("hosted: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var oe oauthError
		if json.Unmarshal(body, &oe) == nil && oe.Code != "" {
			return tokenResponse{}, &oe
		}
		return tokenResponse{}, fmt.Errorf("hosted: token endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return tokenResponse{}, fmt.Errorf("hosted: decode token response: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return tokenResponse{}, fmt.Errorf("hosted: token response missing access or refresh token")
	}
	return tok, nil
}

// credentialFromToken maps a token response onto a stored Credential, stamping
// the access-token expiry from expires_in when present.
func credentialFromToken(server string, tok tokenResponse) Credential {
	c := Credential{
		ServerURL:    server,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Scope:        tok.Scope,
	}
	if tok.ExpiresIn > 0 {
		c.ExpiresAt = nowUTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	c.CreatedAt = nowUTC().Format(time.RFC3339)
	return c
}
