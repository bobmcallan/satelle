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
	// GET /api/v1/workspaces (epic:scoped-sync, order:4) — the caller's personal
	// workspace plus a team workspace `satelle login --workspace` can select.
	mux.HandleFunc("/api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "w1", "kind": "personal", "name": "Dev Personal"},
			{"id": "w2", "kind": "team", "name": "Acme Team"},
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestLoginFlowEndToEnd drives the real binary through a full login against a
// stub server, then whoami and logout — asserting the credential file lands
// OUTSIDE the repo (per-user XDG) and the server lands only in the GLOBAL config.
func TestLoginFlowEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()   // credential home, deliberately outside the repo
	ghome := t.TempDir() // global config home (SATELLE_HOME), isolated from the real ~/.satelle
	mustRun(t, bin, repo, "init")

	ts := stubOAuthServer(t)

	// `login --no-browser` prints the authorize URL and waits on the loopback;
	// the test itself GETs that URL to drive the callback (no real browser).
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SATELLE_HOME="+ghome)
	loginOut := driveLoginNoBrowser(t, bin, repo, ts.URL, env)
	if !strings.Contains(loginOut, "Signed in") {
		t.Error("login did not print the signed-in identity")
	}

	// satelle.toml (committed, in-repo) is left byte-untouched by login — the
	// server is a GLOBAL binding now (sty_53ccf845), never written to the repo file.
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
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

// driveLoginNoBrowser runs `satelle login --no-browser` in repo against server,
// GETs the authorize URL the binary prints to drive the loopback callback (no
// real browser), and returns the captured stdout once login exits. extraArgs are
// appended after the standard flags (e.g. "--workspace"). Shared by the login
// end-to-end tests so the callback-driving plumbing lives once.
func driveLoginNoBrowser(t *testing.T, bin, repo, server string, env []string, extraArgs ...string) string {
	t.Helper()
	args := append([]string{"login", "--no-browser", "--server", server, "--timeout", "20s"}, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = repo
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line)
			buf.WriteByte('\n')
			tl := strings.TrimSpace(line)
			if strings.HasPrefix(tl, "http") && strings.Contains(tl, "/oauth/authorize") {
				if resp, gerr := http.Get(tl); gerr == nil { // 302 → loopback callback delivers the code
					resp.Body.Close()
				}
			}
		}
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("login exited with error: %v", err)
		}
	case <-time.After(25 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("login did not complete in time")
	}
	<-scanDone // let the scanner flush the tail before the caller reads buf
	return buf.String()
}

// TestLoginWorkspaceSelectionEndToEnd proves the AC1/AC4 binding semantics through
// the real binary: `login --workspace <name>` resolves the selection against the
// fetched workspaces and records it in the gitignored per-user satelle.local.toml
// OVERLAY — never the team-committed satelle.toml — while tokens stay in the
// per-user store. A default login (no flag) writes nothing (TestLoginFlowEndToEnd).
func TestLoginWorkspaceSelectionEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")

	ts := stubOAuthServer(t)

	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SATELLE_HOME="+ghome)
	out := driveLoginNoBrowser(t, bin, repo, ts.URL, env, "--workspace", "Acme Team")
	if !strings.Contains(out, "Active workspace set to Acme Team") {
		t.Fatalf("login did not report the workspace selection:\n%s", out)
	}

	// The selection lands in the per-user overlay, not the team satelle.toml.
	local, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.local.toml"))
	if err != nil {
		t.Fatalf("per-user overlay not written: %v", err)
	}
	if !strings.Contains(string(local), `workspace = "Acme Team"`) {
		t.Fatalf("overlay missing the workspace selection:\n%s", local)
	}

	// The committed satelle.toml stays byte-untouched: no workspace, no server,
	// no tokens (the per-user choice never mutates the team file).
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`workspace =`, `server =`, "access_token", "refresh_token", `"acc"`, `"ref"`} {
		if strings.Contains(string(cfg), banned) {
			t.Fatalf("committed satelle.toml must not gain %q:\n%s", banned, cfg)
		}
	}

	// No credential leaked into the repo either.
	if fileExists(filepath.Join(repo, ".satelle", "credentials.toml")) {
		t.Error("credentials must NOT be written inside the repo")
	}
}

// TestLoginWorkspaceNotFoundEndToEnd proves an unresolvable --workspace surfaces a
// clear human error AFTER auth completed (tokens + server are saved) — never a raw
// server body — and records nothing (AC4).
func TestLoginWorkspaceNotFoundEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")

	ts := stubOAuthServer(t)

	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SATELLE_HOME="+ghome)
	cmd := exec.Command(bin, "login", "--no-browser", "--server", ts.URL, "--timeout", "20s", "--workspace", "Nope WS")
	cmd.Dir = repo
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			tl := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(tl, "http") && strings.Contains(tl, "/oauth/authorize") {
				if resp, gerr := http.Get(tl); gerr == nil {
					resp.Body.Close()
				}
			}
		}
	}()
	exitErr := cmd.Wait() // expected non-zero: the workspace choice is unresolvable
	if exitErr == nil {
		t.Fatal("login with an unknown --workspace should exit non-zero")
	}

	// Auth already completed: the credential + global server landed, so the user
	// can retry with a valid name without re-authenticating.
	credPath := filepath.Join(xdg, "satelle", "credentials.toml")
	if cred, rerr := os.ReadFile(credPath); rerr != nil || !strings.Contains(string(cred), "ref") {
		t.Fatalf("auth should have completed before the workspace error: %v", rerr)
	}
	// No overlay is recorded for a failed selection, and the team file is clean.
	if _, lerr := os.Stat(filepath.Join(repo, ".satelle", "satelle.local.toml")); lerr == nil {
		t.Error("no per-user overlay should be written for an unresolvable --workspace")
	}
}
