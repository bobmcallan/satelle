package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestGlobalSettingsWriteGate proves the global settings POST is loopback+CSRF
// gated (like the repo settings POST) and, when accepted, writes the machine-wide
// hosted server (sty_432bdeb7).
func TestGlobalSettingsWriteGate(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	srv, _ := newServer(t)

	form := url.Values{"server": {"https://global.example"}}

	// Rejected: no CSRF header → 403, nothing written.
	resp, err := http.PostForm(srv.URL+"/settings/global", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("write without CSRF header: got %d, want 403", resp.StatusCode)
	}
	if gc, _ := config.LoadGlobal(); gc.Hosted.ResolveServer() != "" {
		t.Fatalf("a rejected write must not persist: %+v", gc.Hosted)
	}

	// Accepted: loopback (httptest RemoteAddr is 127.0.0.1) + CSRF header.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/settings/global", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Satelle-Settings", "1")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode >= 400 {
		t.Fatalf("accepted write returned %d", resp2.StatusCode)
	}
	gc, _ := config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "https://global.example" {
		t.Fatalf("accepted write did not land in the global config: %+v", gc.Hosted)
	}
}

// TestRemoveServerClearsGlobalHostedServer proves the account-menu "Remove server"
// quick action end-to-end (sty_2faa7dd4): a loopback+CSRF POST to /settings/global
// with a BLANK server (exactly what the .acct-remove-form submits) clears the
// machine-wide [hosted] server. This is the destructive clear path the account menu
// exposes — distinct from TestGlobalSettingsWriteGate, which only proves SETTING a value.
func TestRemoveServerClearsGlobalHostedServer(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	srv, _ := newServer(t)

	// Seed a configured server, then confirm it is present.
	if err := config.SaveGlobalHostedServer("https://global.example"); err != nil {
		t.Fatal(err)
	}
	if gc, _ := config.LoadGlobal(); gc.Hosted.ResolveServer() != "https://global.example" {
		t.Fatalf("precondition: hosted server not seeded: %+v", gc.Hosted)
	}

	post := func(server string) int {
		form := url.Values{"server": {server}}
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/settings/global", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Satelle-Settings", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A blank server (the "remove server" action) is gated too: without the CSRF
	// header it is a 403 and the server stays configured.
	form := url.Values{"server": {""}}
	resp, err := http.PostForm(srv.URL+"/settings/global", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("blank write without CSRF header: got %d, want 403", resp.StatusCode)
	}
	if gc, _ := config.LoadGlobal(); gc.Hosted.ResolveServer() != "https://global.example" {
		t.Fatalf("a rejected clear must not wipe the server: %+v", gc.Hosted)
	}

	// The accepted blank POST clears the hosted server from the global config.
	if code := post(""); code >= 400 {
		t.Fatalf("accepted remove-server POST returned %d", code)
	}
	if gc, _ := config.LoadGlobal(); gc.Hosted.ResolveServer() != "" {
		t.Fatalf("remove-server did not clear the hosted server: %+v", gc.Hosted)
	}
}

// TestGlobalSettingsPageRenders confirms the page renders the shared navbar and the
// hosted-server field.
func TestGlobalSettingsPageRenders(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	srv, _ := newServer(t)
	code, body := get(t, srv.URL+"/settings/global")
	if code != 200 {
		t.Fatalf("/settings/global = %d", code)
	}
	for _, want := range []string{`<header class="topbar">`, `name="server"`, "global settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("global settings page missing %q", want)
		}
	}
}
