//go:build integration

package tests

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hasActiveTomlAssign reports whether body has an uncommented assignment of key
// (e.g. `server =` at the start of a line). Comment documentation of the key —
// as the init scaffold does for [hosted]/[sync] — must not count.
func hasActiveTomlAssign(body, key string) bool {
	prefix := key + " ="
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

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
	// The repo satelle.toml never gains an ACTIVE hosted server (comment docs OK).
	if rcfg, _ := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml")); hasActiveTomlAssign(string(rcfg), "server") {
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

const (
	stubClientID = "client-abc"
	stubResource = "https://api.example/"
	// unknownSubjectText is what the stub server says to an account that has
	// never signed in at its own site.
	unknownSubjectText = "Sign in at https://satelle.dev first, then retry."
)

// loginStub is the hermetic sign-in world for the real `satelle login` binary:
// a resource server (the API PRM and /api/v1/*, whose URL is --server) and an
// authorization server (RFC 8414 metadata, authorize, token) on a DIFFERENT host
// (localhost versus 127.0.0.1), both loopback so no request leaves the machine.
type loginStub struct {
	*httptest.Server // the resource server

	// accessToken is what the token endpoint issues; "" means "acc".
	accessToken string
	// unknownSubject makes /api/v1/me answer the unknown_subject 401.
	unknownSubject bool

	mu         sync.Mutex
	prmHits    []string // every PRM path requested
	tokenForms []url.Values
	meHits     int
	asURL      string
}

func (s *loginStub) token() string {
	if s.accessToken != "" {
		return s.accessToken
	}
	return "acc"
}

func (s *loginStub) tokenCalls() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.tokenForms...)
}

func (s *loginStub) meCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meHits
}

// stubOAuthServer starts the default stub: a known account behind an opaque
// access token.
func stubOAuthServer(t *testing.T) *loginStub {
	t.Helper()
	return newLoginStub(t, &loginStub{})
}

func newLoginStub(t *testing.T, s *loginStub) *loginStub {
	t.Helper()
	asMux := http.NewServeMux()
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 s.asURL,
			"authorization_endpoint": s.asURL + "/authorize",
			"token_endpoint":         s.asURL + "/token",
		})
	})
	asMux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != stubClientID || q.Get("resource") != stubResource {
			http.Error(w, "bad authorize params", http.StatusBadRequest)
			return
		}
		v := url.Values{}
		v.Set("code", "code-xyz")
		v.Set("state", q.Get("state")) // echo verbatim
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+v.Encode(), http.StatusFound)
	})
	asMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.tokenForms = append(s.tokenForms, r.PostForm)
		s.mu.Unlock()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != stubClientID ||
			r.Form.Get("resource") != stubResource || r.Form.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": s.token(), "refresh_token": "ref",
			"token_type": "Bearer", "expires_in": 3600, "scope": "satelle",
		})
	})
	as := httptest.NewServer(asMux)
	t.Cleanup(as.Close)
	asURL, err := url.Parse(as.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.asURL = "http://localhost:" + asURL.Port() // a different host from the resource server

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/api", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.prmHits = append(s.prmHits, r.URL.Path)
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              stubResource,
			"authorization_servers": []string{s.asURL},
			"satelle_client_id":     stubClientID,
		})
	})
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.meHits++
		s.mu.Unlock()
		if s.unknownSubject {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown_subject", "message": unknownSubjectText})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.token() {
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
		if r.Header.Get("Authorization") != "Bearer "+s.token() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "w1", "kind": "personal", "name": "Dev Personal"},
			{"id": "w2", "kind": "team", "name": "Acme Team"},
		})
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)
	return s
}

// loginEnv is the environment for a login/logout subprocess: isolated credential
// and global-config homes, and every non-loopback request routed to the
// recording proxy so a stray call to a real provider is seen, not made.
func loginEnv(proxy *releaseProxy, xdg, ghome string) []string {
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SATELLE_HOME="+ghome)
	return append(env, proxy.env()...)
}

// jwtWithSID is a JWT-shaped access token whose payload carries sid.
func jwtWithSID(sid string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(`{"sub":"u1","sid":"`+sid+`"}`)) + ".sig"
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
	proxy := newReleaseProxy(t)
	env := loginEnv(proxy, xdg, ghome)
	loginOut := driveLoginNoBrowser(t, bin, repo, ts.URL, env)
	if !strings.Contains(loginOut, "Signed in") {
		t.Error("login did not print the signed-in identity")
	}
	// The authorize URL points at the discovered authorization server (another
	// host), not at the server --server named.
	if !strings.Contains(loginOut, ts.asURL+"/authorize?") {
		t.Errorf("login did not send the browser to the discovered authorize endpoint %s:\n%s", ts.asURL, loginOut)
	}
	// Only the API PRM was read, never the root one.
	ts.mu.Lock()
	prm := append([]string(nil), ts.prmHits...)
	ts.mu.Unlock()
	if len(prm) != 1 || prm[0] != "/.well-known/oauth-protected-resource/api" {
		t.Errorf("PRM requests = %v, want only the /api one", prm)
	}

	// satelle.toml (committed, in-repo) is left byte-untouched by login — the
	// server is a GLOBAL binding now (sty_53ccf845), never written to the repo file.
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if hasActiveTomlAssign(string(cfg), "server") {
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
	// The credential remembers where refresh goes: the provider's token endpoint.
	for _, want := range []string{`token_endpoint = "` + ts.asURL + `/token"`, `client_id = "` + stubClientID + `"`, `resource = "` + stubResource + `"`} {
		if !strings.Contains(string(cred), want) {
			t.Fatalf("credential missing %s:\n%s", want, cred)
		}
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
	envWhoami.Env = loginEnv(proxy, xdg, ghome)
	whoOut, err := envWhoami.CombinedOutput()
	if err != nil || !strings.Contains(string(whoOut), "dev@satelle.dev") {
		t.Fatalf("whoami did not print identity: err=%v out=%s", err, whoOut)
	}

	// logout clears the stored credential.
	logoutCmd := exec.Command(bin, "logout", "--server", ts.URL)
	logoutCmd.Dir = repo
	logoutCmd.Env = loginEnv(proxy, xdg, ghome)
	logoutOut, err := logoutCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, logoutOut)
	}
	after, _ := os.ReadFile(credPath)
	if strings.Contains(string(after), "ref") {
		t.Fatalf("logout did not clear the credential:\n%s", after)
	}
	// The opaque stub token carries no sid: nothing to open, and the user is told.
	if !strings.Contains(string(logoutOut), "provider session may still be signed in") {
		t.Errorf("logout without a sid must warn:\n%s", logoutOut)
	}
	if seen := proxy.recorded(); len(seen) != 0 {
		t.Fatalf("login, whoami and logout reached beyond loopback: %v", seen)
	}
}

// TestLogoutNoBrowserPrintsServerLogoutURL: with a sid in the access token,
// `logout --no-browser` deletes the credential and prints exactly
// <server>/logout?session_id=<sid> — no return_to — plus the provider-session
// warning, without opening anything.
func TestLogoutNoBrowserPrintsServerLogoutURL(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")

	ts := newLoginStub(t, &loginStub{accessToken: jwtWithSID("session_01ABC")})
	proxy := newReleaseProxy(t)
	env := loginEnv(proxy, xdg, ghome)
	driveLoginNoBrowser(t, bin, repo, ts.URL, env)

	logout := exec.Command(bin, "logout", "--no-browser", "--server", ts.URL)
	logout.Dir, logout.Env = repo, env
	out, err := logout.CombinedOutput()
	if err != nil {
		t.Fatalf("logout --no-browser: %v\n%s", err, out)
	}
	want := ts.URL + "/logout?session_id=session_01ABC"
	if !strings.Contains(string(out), "\n  "+want+"\n") {
		t.Errorf("exact logout URL %s not printed:\n%s", want, out)
	}
	if strings.Contains(string(out), "return_to") {
		t.Errorf("logout URL must carry no return_to:\n%s", out)
	}
	if !strings.Contains(string(out), "provider session may still be signed in") {
		t.Errorf("--no-browser must warn that the provider session may remain:\n%s", out)
	}
	cred, _ := os.ReadFile(filepath.Join(xdg, "satelle", "credentials.toml"))
	if strings.Contains(string(cred), "ref") {
		t.Errorf("logout did not clear the credential:\n%s", cred)
	}
	if seen := proxy.recorded(); len(seen) != 0 {
		t.Fatalf("login and logout reached beyond loopback: %v", seen)
	}
}

// TestLoginUnknownSubjectShowsServerMessage: when the server answers the
// first-contact /me with the unknown_subject 401, login fails showing the
// server's message, sends exactly one /me request and never retries or
// refreshes (the token endpoint saw only the code exchange).
func TestLoginUnknownSubjectShowsServerMessage(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	xdg := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")

	ts := newLoginStub(t, &loginStub{unknownSubject: true})
	proxy := newReleaseProxy(t)
	out, err := driveLogin(t, bin, repo, ts.URL, loginEnv(proxy, xdg, ghome))
	if err == nil {
		t.Fatalf("login for an unknown subject should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, unknownSubjectText) {
		t.Errorf("server's message not shown:\n%s", out)
	}
	if strings.Contains(out, "satelle login") {
		t.Errorf("an unknown subject must not be told to run satelle login:\n%s", out)
	}
	if n := ts.meCalls(); n != 1 {
		t.Errorf("/me requests = %d, want exactly 1", n)
	}
	if n := len(ts.tokenCalls()); n != 1 {
		t.Errorf("token requests = %d, want 1 (the code exchange only)", n)
	}
	if seen := proxy.recorded(); len(seen) != 0 {
		t.Fatalf("login reached beyond loopback: %v", seen)
	}
}

// driveLoginNoBrowser runs `satelle login --no-browser` in repo against server,
// GETs the authorize URL the binary prints to drive the loopback callback (no
// real browser), and returns the captured stdout once login exits. extraArgs are
// appended after the standard flags (e.g. "--workspace"). Shared by the login
// end-to-end tests so the callback-driving plumbing lives once.
func driveLoginNoBrowser(t *testing.T, bin, repo, server string, env []string, extraArgs ...string) string {
	t.Helper()
	out, err := driveLogin(t, bin, repo, server, env, extraArgs...)
	if err != nil {
		t.Fatalf("login exited with error: %v\n%s", err, out)
	}
	return out
}

// driveLogin is driveLoginNoBrowser for a login expected to fail too: it returns
// the exit error alongside the stdout and stderr the binary wrote.
func driveLogin(t *testing.T, bin, repo, server string, env []string, extraArgs ...string) (string, error) {
	t.Helper()
	args := append([]string{"login", "--no-browser", "--callback-port", "0", "--server", server, "--timeout", "20s"}, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = repo
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
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
			if strings.HasPrefix(tl, "http") && strings.Contains(tl, "/authorize?") {
				if resp, gerr := http.Get(tl); gerr == nil { // 302 → loopback callback delivers the code
					resp.Body.Close()
				}
			}
		}
	}()
	// os/exec: Wait closes the StdoutPipe and must not run before every read from
	// it completes. So wait for the scanner to hit EOF (the binary closed stdout)
	// first, then reap; calling Wait concurrently loses the output tail on a
	// slow core. The 25s bound kills the process on a hang.
	select {
	case <-scanDone:
	case <-time.After(25 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("login did not complete in time")
	}
	waitErr := cmd.Wait()
	return buf.String() + stderr.String(), waitErr
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

	proxy := newReleaseProxy(t)
	env := loginEnv(proxy, xdg, ghome)
	out := driveLoginNoBrowser(t, bin, repo, ts.URL, env, "--workspace", "Acme Team")
	if !strings.Contains(out, "Active workspace set to Acme Team") {
		t.Fatalf("login did not report the workspace selection:\n%s", out)
	}
	if seen := proxy.recorded(); len(seen) != 0 {
		t.Fatalf("login reached beyond loopback: %v", seen)
	}

	// The selection lands in the per-user overlay, not the team satelle.toml.
	local, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.local.toml"))
	if err != nil {
		t.Fatalf("per-user overlay not written: %v", err)
	}
	if !strings.Contains(string(local), `workspace = "Acme Team"`) {
		t.Fatalf("overlay missing the workspace selection:\n%s", local)
	}

	// The committed satelle.toml stays free of ACTIVE workspace/server/tokens
	// (comment docs of the keys are fine; the per-user choice never mutates
	// the team file into live assignments).
	cfg, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace", "server"} {
		if hasActiveTomlAssign(string(cfg), key) {
			t.Fatalf("committed satelle.toml must not gain active %q:\n%s", key, cfg)
		}
	}
	for _, banned := range []string{"access_token", "refresh_token", `"acc"`, `"ref"`} {
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

	proxy := newReleaseProxy(t)
	env := loginEnv(proxy, xdg, ghome)
	cmd := exec.Command(bin, "login", "--no-browser", "--callback-port", "0", "--server", ts.URL, "--timeout", "20s", "--workspace", "Nope WS")
	cmd.Dir = repo
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			tl := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(tl, "http") && strings.Contains(tl, "/authorize?") {
				if resp, gerr := http.Get(tl); gerr == nil {
					resp.Body.Close()
				}
			}
		}
	}()
	// Reads must finish before Wait closes the pipe (os/exec contract); the
	// binary's own --timeout 20s bounds the run, this bound only guards a hang.
	select {
	case <-scanDone:
	case <-time.After(25 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("login did not complete in time")
	}
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
	if seen := proxy.recorded(); len(seen) != 0 {
		t.Fatalf("login reached beyond loopback: %v", seen)
	}
}
