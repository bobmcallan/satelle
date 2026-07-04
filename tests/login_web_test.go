//go:build integration

package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWebLoginSignedOutAffordance drives the real served binary in a repo with no
// [hosted] server and confirms the topbar renders the "Sign in" control and the
// /oauth/login route degrades to a friendly not-configured page — the additive,
// standalone-safe half of sty_9ae98484.
func TestWebLoginSignedOutAffordance(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const port = "8796"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir()) // isolated, empty cred store
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	body := httpGet(t, base+"/")
	if !strings.Contains(body, `class="signin"`) || !strings.Contains(body, `href="oauth/login"`) {
		t.Fatalf("signed-out topbar missing Sign in control")
	}
	if strings.Contains(body, "account-menu") {
		t.Fatal("unconfigured repo should not render an account menu")
	}
	// /oauth/login on an unconfigured repo is a friendly 200, not an error.
	if code := httpStatus(t, base+"/oauth/login"); code != 200 {
		t.Fatalf("/oauth/login unconfigured = %d, want 200", code)
	}
}

// TestWebLoginSignedInAvatar drives the served binary configured for a stub
// hosted server with a seeded credential in the per-user store, and confirms the
// topbar renders the avatar + identity from GET /api/v1/me — proving the web UI
// and CLI share one credential store.
func TestWebLoginSignedInAvatar(t *testing.T) {
	// Stub hosted server: /api/v1/me returns a principal for the seeded token.
	me := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/me" && r.Header.Get("Authorization") == "Bearer acc" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": "u1", "email": "dev@satelle.dev", "display_name": "Dev User", "role": "admin",
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer me.Close()

	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Configure the repo for the stub server.
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	cfg, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, append(cfg, []byte("\n[hosted]\nserver = \""+me.URL+"\"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed a credential in an isolated per-user store.
	xdg := t.TempDir()
	credDir := filepath.Join(xdg, "satelle")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Identity is stamped at login and read locally on render (sty_467c6944).
	credToml := "[[credential]]\nserver_url = \"" + me.URL + "\"\naccess_token = \"acc\"\nrefresh_token = \"ref\"\ndisplay_name = \"Dev User\"\nemail = \"dev@satelle.dev\"\n"
	if err := os.WriteFile(filepath.Join(credDir, "credentials.toml"), []byte(credToml), 0o600); err != nil {
		t.Fatal(err)
	}

	const port = "8797"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	body := httpGet(t, base+"/")
	for _, want := range []string{`class="avatar"`, "Dev User", "dev@satelle.dev", `action="oauth/logout"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("signed-in topbar missing %q", want)
		}
	}
	if strings.Contains(body, `class="signin"`) {
		t.Fatal("signed-in topbar should not show Sign in")
	}

	// Sign out via the real POST, then confirm the credential is gone from the
	// shared store and the served page reverts to the Sign in control (AC2).
	resp, err := http.Post(base+"/oauth/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST logout: %v", err)
	}
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(credDir, "credentials.toml")); err == nil {
		if b, _ := os.ReadFile(filepath.Join(credDir, "credentials.toml")); strings.Contains(string(b), "ref") {
			t.Fatalf("logout did not clear the credential:\n%s", b)
		}
	}
	after := httpGet(t, base+"/")
	if !strings.Contains(after, `class="signin"`) {
		t.Fatalf("after logout the topbar should show Sign in:\n%s", after)
	}
}
