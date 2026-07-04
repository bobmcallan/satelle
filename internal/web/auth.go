package web

// Optional hosted-server sign-in for the web UI (sty_9ae98484). The local web
// server runs the SAME OAuth 2.1 + PKCE flow as the CLI (internal/hosted),
// SPLIT across two requests: /oauth/login sends the browser to the authorization
// server; /oauth/callback verifies state, exchanges the code, and persists tokens
// to the per-user XDG credential store the CLI also writes — so `satelle whoami`
// and the web UI agree. All additive: with no [hosted] server configured (or the
// server unreachable) the UI simply shows a "Sign in" affordance and never errors,
// and page renders NEVER block on the network (identity is cache + local file).

import (
	"context"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// hostedServer is the configured hosted-server base URL (from [hosted] server),
// normalized, or "" when unconfigured. Set by New via setHostedServer.
var hostedServer string

func setHostedServer(raw string) {
	hostedServer = strings.TrimRight(strings.TrimSpace(raw), "/")
}

// pendingFlow is one in-flight sign-in, keyed by its CSRF state until the
// callback consumes it (one-shot) or it expires.
type pendingFlow struct {
	flow    *hosted.Flow
	created time.Time
}

var (
	pendingMu sync.Mutex
	pending   = map[string]*pendingFlow{}
)

const pendingTTL = 10 * time.Minute

// Principal cache: keeps a page render from making a network call on every hit.
var (
	principalMu   sync.Mutex
	cachedPrinc   *hosted.Principal
	principalAt   time.Time
	principalMiss time.Time
)

const (
	principalTTL     = 60 * time.Second
	principalMissTTL = 30 * time.Second
)

// topBarUser is the signed-in identity the topbar renders.
type topBarUser struct {
	Name    string
	Email   string
	Initial string
}

// newTopBar builds the shared topbar data, including the signed-in identity when
// there is a usable credential. It never blocks a render on the network beyond a
// single short, cached-on-failure identity fetch.
func newTopBar() topBar {
	return topBar{Uptime: formatUptime(time.Since(serverStart)), User: resolveUser()}
}

// resolveUser returns the signed-in identity, or nil (render "Sign in"). Order:
// unconfigured → nil; no local credential → nil; fresh cache → it; else ONE
// bounded Me() fetch (its failure is cached briefly so a down server does not
// stall or hammer every render).
func resolveUser() *topBarUser {
	if hostedServer == "" {
		return nil
	}
	store := hosted.FileStore{}
	if _, err := store.Load(hostedServer); err != nil {
		return nil // no credential for this server → signed out
	}

	principalMu.Lock()
	if cachedPrinc != nil && time.Since(principalAt) < principalTTL {
		u := topBarUserFrom(*cachedPrinc)
		principalMu.Unlock()
		return u
	}
	if time.Since(principalMiss) < principalMissTTL {
		principalMu.Unlock()
		return nil // recently failed — show Sign in this render, don't re-hit
	}
	principalMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	who, err := hosted.NewClient(hostedServer, store, nil).Me(ctx)
	principalMu.Lock()
	defer principalMu.Unlock()
	if err != nil {
		principalMiss = time.Now()
		return nil
	}
	cachedPrinc = &who
	principalAt = time.Now()
	return topBarUserFrom(who)
}

func topBarUserFrom(p hosted.Principal) *topBarUser {
	name := p.DisplayName
	if strings.TrimSpace(name) == "" {
		name = p.Email
	}
	initial := "?"
	for _, r := range name {
		initial = strings.ToUpper(string(r))
		break
	}
	return &topBarUser{Name: name, Email: p.Email, Initial: initial}
}

func clearPrincipalCache() {
	principalMu.Lock()
	cachedPrinc, principalMiss = nil, time.Time{}
	principalMu.Unlock()
}

// callbackURI builds the redirect target from the browser-facing host and the
// server's base path — http://<host>[/<slug>]/oauth/callback. Under the
// supervisor the reverse proxy preserves r.Host, and baseHref carries the slug,
// so the child registers a redirect the authorization server accepts (loopback,
// any port, any path — verified against satelle-server).
func callbackURI(r *http.Request) string {
	return "http://" + r.Host + baseHref() + "oauth/callback"
}

// oauthLogin starts a sign-in: register a fresh flow by state, 302 to authorize.
func oauthLogin(w http.ResponseWriter, r *http.Request) {
	if hostedServer == "" {
		authPage(w, http.StatusOK, "Not configured",
			"No hosted server is configured for this repo. Set <code>[hosted] server</code> in <code>.satelle/satelle.toml</code> (or run <code>satelle login --server &lt;url&gt;</code>) to enable sign-in.")
		return
	}
	flow, err := hosted.NewFlow(hostedServer, callbackURI(r))
	if err != nil {
		authPage(w, http.StatusInternalServerError, "Sign-in error", html.EscapeString(err.Error()))
		return
	}
	pendingMu.Lock()
	prunePendingLocked()
	pending[flow.State()] = &pendingFlow{flow: flow, created: time.Now()}
	pendingMu.Unlock()
	http.Redirect(w, r, flow.AuthorizeURL(), http.StatusFound)
}

// oauthCallback verifies state, exchanges the code, and persists the credential.
// An unknown state or an error= param aborts WITHOUT ever calling the token
// endpoint.
func oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")

	pendingMu.Lock()
	pf := pending[state]
	delete(pending, state) // one-shot regardless of outcome
	pendingMu.Unlock()

	if state == "" || pf == nil {
		authPage(w, http.StatusBadRequest, "Sign-in failed",
			"This sign-in link is unrecognised or expired (state mismatch). Please start again.")
		return
	}
	if e := q.Get("error"); e != "" {
		authPage(w, http.StatusBadRequest, "Sign-in failed", "Authorization error: "+html.EscapeString(e))
		return
	}
	code := q.Get("code")
	if code == "" {
		authPage(w, http.StatusBadRequest, "Sign-in failed", "No authorization code was returned.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cred, err := pf.flow.Exchange(ctx, nil, code)
	if err != nil {
		authPage(w, http.StatusBadGateway, "Sign-in failed", "Token exchange failed: "+html.EscapeString(err.Error()))
		return
	}
	if err := (hosted.FileStore{}).Save(cred); err != nil {
		authPage(w, http.StatusInternalServerError, "Sign-in failed", "Could not store the credential: "+html.EscapeString(err.Error()))
		return
	}
	clearPrincipalCache() // force a fresh identity for the new session
	http.Redirect(w, r, baseHref(), http.StatusFound)
}

// oauthLogout clears the stored credential and returns to the page.
func oauthLogout(w http.ResponseWriter, r *http.Request) {
	if hostedServer != "" {
		_ = (hosted.FileStore{}).Delete(hostedServer)
	}
	clearPrincipalCache()
	http.Redirect(w, r, baseHref(), http.StatusFound)
}

// prunePendingLocked drops expired pending flows. Caller holds pendingMu.
func prunePendingLocked() {
	for s, pf := range pending {
		if time.Since(pf.created) > pendingTTL {
			delete(pending, s)
		}
	}
}

// authPage renders a minimal status page for the sign-in routes (success just
// redirects, so this is only the informational/error surface).
func authPage(w http.ResponseWriter, status int, title, msgHTML string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<!doctype html><html><body style=\"font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;line-height:1.5\"><h2>" +
		html.EscapeString(title) + "</h2><p>" + msgHTML + "</p><p><a href=\"" + baseHref() + "\">← back</a></p></body></html>"))
}
