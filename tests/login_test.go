//go:build integration

package tests

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSettingsServerCLIEndToEnd drives the real binary: `satelle settings server`
// configures the machine-wide hosted server WITHOUT a login, writing only the
// global config (never the repo satelle.toml, never a token) — the decoupled
// config path the user asked for (sty_432bdeb7).
func TestSettingsServerCLIEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")
	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "SATELLE_HOME="+ghome)
		out, err := c.CombinedOutput()
		return string(out), err
	}

	if out, err := run("settings", "server", "https://demo.example"); err != nil {
		t.Fatalf("settings server set: %v\n%s", err, out)
	}
	// Server landed in the GLOBAL config, no token.
	gcfg, err := os.ReadFile(filepath.Join(ghome, "config.toml"))
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	if !strings.Contains(string(gcfg), "https://demo.example") {
		t.Fatalf("global config missing the server:\n%s", gcfg)
	}
	if strings.Contains(string(gcfg), "token") {
		t.Fatalf("global config must not contain a token:\n%s", gcfg)
	}
	// The repo satelle.toml never gains a hosted server.
	if rcfg, _ := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml")); strings.Contains(string(rcfg), "server =") {
		t.Fatalf("repo satelle.toml must NOT gain a hosted server:\n%s", rcfg)
	}
	// Print shows it.
	if out, _ := run("settings", "server"); !strings.Contains(out, "https://demo.example") {
		t.Fatalf("settings server print: %s", out)
	}
	// Clear removes it.
	if out, err := run("settings", "server", "--clear"); err != nil {
		t.Fatalf("settings server --clear: %v\n%s", err, out)
	}
	if out, _ := run("settings", "server"); !strings.Contains(out, "no global hosted server") {
		t.Fatalf("server not cleared: %s", out)
	}
}

// stubOAuthServer is a hermetic OAuth 2.1 + PKCE authorization server plus a
// /api/v1/me principal endpoint, for driving the real `satelle login` binary.
func stubOAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "satelle-cli" {
			http.Error(w, "bad authorize params", http.StatusBadRequest)
			return
		}
		v := url.Values{}
		v.Set("code", "code-xyz")
		v.Set("state", q.Get("state")) // echo verbatim
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+v.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "satelle-cli" || r.Form.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc", "refresh_token": "ref",
			"token_type": "Bearer", "expires_in": 3600, "scope": "satelle",
		})
	})
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": "u1", "email": "dev@satelle.dev", "display_name": "Dev", "role": "owner",
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestLoginFlowEndToEnd drives the real binary through a full login against a
// stub server, then whoami and logout — asserting the committed satelle.toml
// gains [hosted] and the credential file lands OUTSIDE the repo (per-user XDG).
func TestLoginFlowEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()   // credential home, deliberately outside the repo
	ghome := t.TempDir() // global config home (SATELLE_HOME), isolated from the real ~/.satelle
	mustRun(t, bin, repo, "init")

	ts := stubOAuthServer(t)

	// `login --no-browser` prints the authorize URL and waits on the loopback;
	// the test itself GETs that URL to drive the callback (no real browser).
	cmd := exec.Command(bin, "login", "--no-browser", "--server", ts.URL, "--project", "demo", "--timeout", "20s")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SATELLE_HOME="+ghome)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var signedIn bool
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "http") && strings.Contains(line, "/oauth/authorize") {
				resp, gerr := http.Get(line) // 302 → loopback callback delivers the code
				if gerr == nil {
					resp.Body.Close()
				}
			}
			if strings.Contains(line, "Signed in") {
				signedIn = true
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("login exited with error: %v", err)
		}
	case <-time.After(25 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("login did not complete in time")
	}
	if !signedIn {
		t.Error("login did not print the signed-in identity")
	}

	// satelle.toml (committed, in-repo) gained ONLY the project slug — the server
	// is a GLOBAL binding now (sty_53ccf845), never written to the repo file.
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `project = "demo"`) {
		t.Fatalf("satelle.toml missing the project slug:\n%s", cfg)
	}
	if strings.Contains(string(cfg), "server =") {
		t.Fatalf("committed satelle.toml must NOT gain a hosted server (it is global now):\n%s", cfg)
	}
	if strings.Contains(string(cfg), "access_token") || strings.Contains(string(cfg), "refresh_token") || strings.Contains(string(cfg), `"acc"`) || strings.Contains(string(cfg), `"ref"`) {
		t.Fatalf("committed satelle.toml must not contain tokens:\n%s", cfg)
	}

	// The server landed in the GLOBAL config (~/.satelle/config.toml via SATELLE_HOME),
	// so one sign-in serves every repo. No tokens there either.
	gcfg, err := os.ReadFile(filepath.Join(ghome, "config.toml"))
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	if !strings.Contains(string(gcfg), ts.URL) {
		t.Fatalf("global config missing the hosted server %q:\n%s", ts.URL, gcfg)
	}
	if strings.Contains(string(gcfg), `"acc"`) || strings.Contains(string(gcfg), `"ref"`) || strings.Contains(string(gcfg), "token") {
		t.Fatalf("global config must not contain tokens:\n%s", gcfg)
	}

	// The credential file landed under XDG (outside the repo), not in the repo.
	credPath := filepath.Join(xdg, "satelle", "credentials.toml")
	cred, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("credential file not at %s: %v", credPath, err)
	}
	if !strings.Contains(string(cred), "ref") {
		t.Fatalf("refresh token not persisted:\n%s", cred)
	}
	// Login stamps the principal into the credential so the web UI resolves
	// identity locally, with no render-time fetch (sty_467c6944).
	if !strings.Contains(string(cred), "dev@satelle.dev") || !strings.Contains(string(cred), "display_name") {
		t.Fatalf("identity not persisted at login:\n%s", cred)
	}
	if fileExists(filepath.Join(repo, ".satelle", "credentials.toml")) {
		t.Error("credentials must NOT be written inside the repo")
	}

	// whoami reads back the principal via the built binary (XDG points at the
	// per-user store the login wrote).
	envWhoami := exec.Command(bin, "whoami", "--server", ts.URL)
	envWhoami.Dir = repo
	envWhoami.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	whoOut, err := envWhoami.CombinedOutput()
	if err != nil || !strings.Contains(string(whoOut), "dev@satelle.dev") {
		t.Fatalf("whoami did not print identity: err=%v out=%s", err, whoOut)
	}

	// logout clears the stored credential.
	logoutCmd := exec.Command(bin, "logout", "--server", ts.URL)
	logoutCmd.Dir = repo
	logoutCmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	if out, err := logoutCmd.CombinedOutput(); err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(credPath)
	if strings.Contains(string(after), "ref") {
		t.Fatalf("logout did not clear the credential:\n%s", after)
	}
}
