package web

// Optional hosted-server sign-in for the web UI (sty_9ae98484). The local web
// server runs the SAME OAuth 2.1 + PKCE flow as the CLI (internal/hosted),
// SPLIT across two requests: /oauth/login sends the browser to the authorization
// server; /oauth/callback verifies state, exchanges the code, and persists tokens
// to the per-user XDG credential store the CLI also writes — so `satelle whoami`
// and the web UI agree. All additive: with no [hosted] server configured (or the
// server unreachable) the UI simply shows a "Sign in" affordance and never errors.
// Page renders NEVER call the network: identity is read from the local credential
// (stamped at login), and a legacy credential self-heals via a background warm
// (sty_467c6944).

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

// Background identity warm for a legacy credential (tokens but no stored
// identity). Single-flight with a min interval so a legacy cred against a down
// server never spawns a goroutine per render (sty_467c6944).
var (
	warmMu   sync.Mutex
	warmBusy bool
	warmLast time.Time
)

const warmMinInterval = 30 * time.Second

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

// resolveUser returns the signed-in identity from the LOCAL credential file —
// NEVER a network call on the render path (sty_467c6944). Unconfigured or no
// credential → nil (render "Sign in"). A credential with a stored identity → it.
// A legacy credential (tokens but no identity) renders signed-out this once and
// triggers a background warm that writes identity back for the next render.
func resolveUser() *topBarUser {
	if hostedServer == "" {
		return nil
	}
	store := hosted.FileStore{}
	cred, err := store.Load(hostedServer)
	if err != nil {
		return nil // no credential for this server → signed out
	}
	if strings.TrimSpace(cred.DisplayName) != "" || strings.TrimSpace(cred.Email) != "" {
		return topBarUserFrom(hosted.Principal{DisplayName: cred.DisplayName, Email: cred.Email})
	}
	warmIdentityAsync(store)
	return nil
}

// warmIdentityAsync fetches the principal once, off the render path, and writes
// it into the credential so a later render resolves locally. Single-flight with
// a min interval so a down server cannot spawn a goroutine per render.
func warmIdentityAsync(store hosted.FileStore) {
	warmMu.Lock()
	if warmBusy || time.Since(warmLast) < warmMinInterval {
		warmMu.Unlock()
		return
	}
	warmBusy = true
	warmMu.Unlock()

	go func() {
		defer func() {
			warmMu.Lock()
			warmBusy, warmLast = false, time.Now()
			warmMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		who, err := hosted.NewClient(hostedServer, store, nil).Me(ctx)
		if err != nil {
			return
		}
		// Re-load so we stamp identity onto the CURRENT credential — Me() may have
		// rotated tokens mid-flight; never resurrect a stale token pair.
		cur, err := store.Load(hostedServer)
		if err != nil {
			return
		}
		cur.DisplayName, cur.Email = who.DisplayName, who.Email
		_ = store.Save(cur)
	}()
}

// resetWarmState clears the background-warm guard (tests).
func resetWarmState() {
	warmMu.Lock()
	warmBusy, warmLast = false, time.Time{}
	warmMu.Unlock()
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
	store := hosted.FileStore{}
	if err := store.Save(cred); err != nil {
		authPage(w, http.StatusInternalServerError, "Sign-in failed", "Could not store the credential: "+html.EscapeString(err.Error()))
		return
	}
	// Stamp the principal so the topbar resolves identity locally with no
	// render-time fetch. Never fail the sign-in over the identity call — a legacy
	// credential self-heals via the background warm.
	if who, mErr := hosted.NewClient(hostedServer, store, nil).Me(ctx); mErr == nil {
		if fresh, lErr := store.Load(hostedServer); lErr == nil {
			fresh.DisplayName, fresh.Email = who.DisplayName, who.Email
			_ = store.Save(fresh)
		}
	}
	http.Redirect(w, r, baseHref(), http.StatusFound)
}

// oauthLogout clears the stored credential and returns to the page.
func oauthLogout(w http.ResponseWriter, r *http.Request) {
	if hostedServer != "" {
		_ = (hosted.FileStore{}).Delete(hostedServer)
	}
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
