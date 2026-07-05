package web

// Global settings page (sty_432bdeb7): a workspace-level editor for the MACHINE-WIDE
// config (~/.satelle/config.toml) — the hosted server and the theme — distinct from
// the per-repo settings page (which edits the committed satelle.toml). Because the
// global config is machine-wide, it does not matter which repo's handler serves this;
// the supervisor falls /settings/* through to the launch repo's handler. Viewing is
// unauthenticated like every page; the WRITE reuses the same loopback + CSRF gate as
// the repo settings POST (settingsWriteAllowed), so the 0.0.0.0-bound view never
// becomes an unauthenticated LAN/CSRF write vector.

import (
	"net"
	"net/http"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// settingsCSRFHeader must be present on a settings write; a cross-origin page cannot
// set a custom header without a CORS preflight this server never grants.
const settingsCSRFHeader = "X-Satelle-Settings"

// settingsWriteAllowed is the settings write gate: the CSRF header, a loopback child
// socket, and (when the supervisor forwarded it) a loopback true client address. The
// per-project settings page is read-only; only the global settings POST writes, so
// this gate lives with it — the 0.0.0.0-bound view never becomes an unauthenticated
// LAN (or CSRF) write vector.
func settingsWriteAllowed(r *http.Request) bool {
	if r.Header.Get(settingsCSRFHeader) != "1" {
		return false
	}
	if !addrIsLoopback(r.RemoteAddr) {
		return false
	}
	if ca := r.Header.Get(ClientAddrHeader); ca != "" && !addrIsLoopback(ca) {
		return false
	}
	return true
}

func addrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

type globalSettingsData struct {
	TopBar topBar
	Server string
	Theme  string // "", "light", or "dark"
	Saved  bool
}

func globalSettingsGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gc, _ := config.LoadGlobal()
		render(w, "globalSettings", globalSettingsData{
			TopBar: newTopBar(""),
			Server: gc.Hosted.ResolveServer(),
			Theme:  gc.UI.Theme,
			Saved:  r.URL.Query().Get("saved") == "1",
		})
	}
}

// globalSettingsPost applies the machine-wide server + theme behind the loopback+CSRF
// gate, then rebinds the live sign-in server so the change takes effect without a
// serve restart.
func globalSettingsPost(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !settingsWriteAllowed(r) {
			http.Error(w, "config writes are accepted only from this machine (loopback) via the settings page", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		// Server: a blank value clears it (the "remove server" action); a value sets it.
		server := strings.TrimSpace(r.FormValue("server"))
		if server == "" {
			if err := config.ClearGlobalHostedServer(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else if err := config.SaveGlobalHostedServer(server); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Theme: only an explicit light/dark is persisted; anything else leaves it.
		if theme := strings.TrimSpace(r.FormValue("theme")); theme == "light" || theme == "dark" {
			if gc, err := config.LoadGlobal(); err == nil {
				gc.UI.Theme = theme
				_ = config.SaveGlobal(gc)
			}
		}
		// Rebind the live sign-in binding (resolved global-first) so the topbar tracks
		// the edited server immediately.
		ncfg, _, _ := config.Load(configPathFor(a))
		setHostedServer(config.ResolveHostedServer(ncfg))
		http.Redirect(w, r, baseHref()+"settings/global?saved=1", http.StatusSeeOther)
	}
}
