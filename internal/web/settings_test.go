package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func settingsRepo(t *testing.T, tomlBody string) *app.App {
	t.Helper()
	testutil.IsolateHome(t)
	repo := t.TempDir()
	dir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.ConfigName), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return &app.App{RepoRoot: repo}
}

// postSettings builds a settings POST with the given remote addr, client-addr
// header, and CSRF header flag.
func postSettings(form url.Values, remoteAddr, clientAddr string, csrf bool) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	if clientAddr != "" {
		req.Header.Set(ClientAddrHeader, clientAddr)
	}
	if csrf {
		req.Header.Set(settingsCSRFHeader, "1")
	}
	return req
}

// TestSettingsWriteGate exercises the shared settings write gate (settingsWriteAllowed,
// now the guard on the GLOBAL settings POST — the per-project page is read-only): only a
// loopback request carrying the CSRF header is allowed, and a supervisor-forwarded
// non-loopback client is rejected.
func TestSettingsWriteGate(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		clientAddr string
		csrf       bool
		wantAllow  bool
	}{
		{"non-loopback direct rejected", "192.0.2.1:1234", "", true, false},
		{"loopback direct allowed", "127.0.0.1:9999", "", true, true},
		{"ipv6 loopback allowed", "[::1]:9999", "", true, true},
		{"proxied LAN client rejected", "127.0.0.1:5000", "192.168.1.50:1", true, false},
		{"proxied loopback client allowed", "127.0.0.1:5000", "127.0.0.1:41000", true, true},
		{"missing csrf header rejected", "127.0.0.1:9999", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := postSettings(url.Values{}, tc.remoteAddr, tc.clientAddr, tc.csrf)
			if got := settingsWriteAllowed(req); got != tc.wantAllow {
				t.Fatalf("settingsWriteAllowed = %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

// TestSettingsPostDoesNotWrite proves AC2: the per-project settings page has NO write
// path. A POST to /settings — even loopback + CSRF, which the removed handler would have
// accepted — is answered 405 by the mux (only GET is registered) and leaves the committed
// satelle.toml byte-for-byte unchanged.
func TestSettingsPostDoesNotWrite(t *testing.T) {
	resetAuthState(t)
	body := "# keep me\nweb_port = 8787\nfuture_key = \"x\"\n\n[review]\ngate_create = true\n"
	a := settingsRepo(t, body)
	cfgPath := filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName)
	before, _ := os.ReadFile(cfgPath)

	form := url.Values{}
	form.Set("web_port", "9100") // the old write path would have persisted this
	rec := httptest.NewRecorder()
	Build(a).ServeHTTP(rec, postSettings(form, "127.0.0.1:5000", "", true))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /settings should be 405 (no write route), got %d", rec.Code)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(before) {
		t.Fatalf("read-only settings must not mutate satelle.toml:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestSettingsGetReadOnly proves AC1/AC3/AC4: the page renders resolved values + help as a
// read-only table (no form, no inputs, no Save), points the user at the file and the global
// settings page, omits hosted.server, and keeps hosted.project.
func TestSettingsGetReadOnly(t *testing.T) {
	resetAuthState(t)
	a := settingsRepo(t, "web_port = 8123\nlog_level = \"debug\"\n\n[hosted]\nproject = \"acme\"\nserver = \"https://should-not-show.example\"\n\n[gate]\nedit_exempt_paths = [\".claude/\"]\n")
	rec := httptest.NewRecorder()
	settingsGet(a)(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	body := rec.Body.String()

	// AC1: resolved values + help + labels render.
	for _, want := range []string{"8123", "debug", "acme", ".claude/", "Web port", "read-only view"} {
		if !strings.Contains(body, want) {
			t.Errorf("read-only settings page missing %q", want)
		}
	}
	// AC1: no REPO-CONFIG write UI — the editor is gone. Viewer-local DISPLAY
	// preferences (localStorage only, e.g. the Timeline-fields checkboxes,
	// sty_43d228e4) are NOT a repo-config write and may appear.
	for _, forbidden := range []string{`<textarea`, "settings-save", `action="settings"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("read-only settings page must not contain the repo-config write control %q", forbidden)
		}
	}
	// The viewer-local Timeline-fields checkboxes must carry no name= (so a stray
	// submit could never post them as server settings) — a display pref, not a write.
	if strings.Contains(body, `<input type="checkbox" name=`) {
		t.Error("a settings-page checkbox must not be a named server form field (viewer prefs are localStorage-only)")
	}
	// AC3: the note points at the file and links the global settings page.
	if !strings.Contains(body, `href="settings/global"`) {
		t.Error("read-only settings page must link to the global settings page")
	}
	if !strings.Contains(body, ".satelle/satelle.toml") {
		t.Error("read-only settings page must tell the user to edit .satelle/satelle.toml directly")
	}
	// AC4: hosted.server absent (now global), hosted.project present.
	if strings.Contains(body, "Hosted server") || strings.Contains(body, "should-not-show") {
		t.Error("hosted.server must not appear on the per-project settings surface")
	}
	if !strings.Contains(body, "Hosted project") {
		t.Error("hosted.project should still render read-only")
	}
}
