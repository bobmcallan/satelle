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
	xdg := t.TempDir() // credential home, deliberately outside the repo
	mustRun(t, bin, repo, "init")

	ts := stubOAuthServer(t)

	// `login --no-browser` prints the authorize URL and waits on the loopback;
	// the test itself GETs that URL to drive the callback (no real browser).
	cmd := exec.Command(bin, "login", "--no-browser", "--server", ts.URL, "--project", "demo", "--timeout", "20s")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
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

	// satelle.toml (committed, in-repo) gained the [hosted] block.
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "[hosted]") || !strings.Contains(string(cfg), "project = \"demo\"") {
		t.Fatalf("satelle.toml missing [hosted]/project:\n%s", cfg)
	}
	if strings.Contains(string(cfg), "access_token") || strings.Contains(string(cfg), "refresh_token") || strings.Contains(string(cfg), `"acc"`) || strings.Contains(string(cfg), `"ref"`) {
		t.Fatalf("committed satelle.toml must not contain tokens:\n%s", cfg)
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
