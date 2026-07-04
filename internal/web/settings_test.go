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
)

func settingsRepo(t *testing.T, tomlBody string) *app.App {
	t.Helper()
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

func TestSettingsPostRoundTripPreservesUnmodeled(t *testing.T) {
	resetAuthState(t)
	a := settingsRepo(t, "# keep me\nweb_port = 8787\nfuture_key = \"x\"\n\n[review]\ngate_create = true\n")

	form := url.Values{}
	form.Set("web_port", "9100")
	form.Set("log_level", "warn")
	form.Set("hosted.server", "https://hosted.example")
	// review.gate_create checkbox omitted → unchecked → false (a change from true).

	rec := httptest.NewRecorder()
	settingsPost(a)(rec, postSettings(form, "127.0.0.1:5000", "", true))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("post status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got, _ := os.ReadFile(filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName))
	if !strings.Contains(string(got), "# keep me") || !strings.Contains(string(got), `future_key = "x"`) {
		t.Fatalf("unmodeled content lost:\n%s", got)
	}
	cfg, _, _ := config.Load(filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName))
	if cfg.WebPort != 9100 || cfg.LogLevel != "warn" || cfg.Hosted.Server != "https://hosted.example" {
		t.Fatalf("edits not applied: %+v", cfg)
	}
	if cfg.Review.GateCreate {
		t.Fatal("gate_create should have flipped to false")
	}
	// The hosted rebind updated the live sign-in server (AC4).
	if hostedServer != "https://hosted.example" {
		t.Fatalf("hostedServer not rebound, got %q", hostedServer)
	}

	// A following GET renders the new values.
	recGet := httptest.NewRecorder()
	settingsGet(a)(recGet, httptest.NewRequest(http.MethodGet, "/settings", nil))
	body := recGet.Body.String()
	if !strings.Contains(body, `value="9100"`) || !strings.Contains(body, `value="warn"`) {
		t.Fatalf("GET did not reflect saved values:\n%s", body)
	}
}

// A partial POST (only some fields) must not blank or add the keys it omitted.
func TestSettingsPostPartialDoesNotBlankOthers(t *testing.T) {
	resetAuthState(t)
	a := settingsRepo(t, "[hosted]\nserver = \"https://keep.example\"\nproject = \"keep\"\n")
	form := url.Values{}
	form.Set("log_level", "warn") // only this field submitted
	rec := httptest.NewRecorder()
	settingsPost(a)(rec, postSettings(form, "127.0.0.1:5000", "", true))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("post status = %d", rec.Code)
	}
	cfg, _, _ := config.Load(filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName))
	if cfg.Hosted.Server != "https://keep.example" || cfg.Hosted.Project != "keep" {
		t.Fatalf("omitted hosted keys were blanked: %+v", cfg.Hosted)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("submitted key not applied: %q", cfg.LogLevel)
	}
	got, _ := os.ReadFile(filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName))
	if strings.Contains(string(got), `data_dir =`) || strings.Contains(string(got), `db =`) {
		t.Fatalf("omitted keys were added:\n%s", got)
	}
}

func TestSettingsPostRejectedWhenNotLoopback(t *testing.T) {
	resetAuthState(t)
	a := settingsRepo(t, "web_port = 8787\n")
	form := url.Values{}
	form.Set("web_port", "9999")
	rec := httptest.NewRecorder()
	settingsPost(a)(rec, postSettings(form, "203.0.113.9:5000", "", true))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	// The file must be untouched.
	got, _ := os.ReadFile(filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName))
	if strings.Contains(string(got), "9999") {
		t.Fatalf("rejected write must not touch the file:\n%s", got)
	}
}

func TestSettingsGetRenders(t *testing.T) {
	resetAuthState(t)
	a := settingsRepo(t, "web_port = 8123\nlog_level = \"debug\"\n")
	rec := httptest.NewRecorder()
	settingsGet(a)(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	body := rec.Body.String()
	for _, want := range []string{"settings", `value="8123"`, `value="debug"`, "Hosted server", "Edit-gate exempt paths"} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
}
