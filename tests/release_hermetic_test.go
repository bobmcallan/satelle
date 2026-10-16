//go:build integration

package tests

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// releaseProxy is a loopback forward proxy that records every request it
// receives (method and host; a CONNECT carries host:port) and refuses all of
// them, so a request that would leave the machine is observed and goes nowhere.
type releaseProxy struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string
}

func newReleaseProxy(t *testing.T) *releaseProxy {
	t.Helper()
	p := &releaseProxy{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.Host)
		p.mu.Unlock()
		http.Error(w, "refused by the recording proxy", http.StatusForbidden)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *releaseProxy) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// sawGitHub reports the first recorded request that names github.com.
func (p *releaseProxy) sawGitHub() (string, bool) {
	for _, s := range p.recorded() {
		if strings.Contains(s, "github.com") {
			return s, true
		}
	}
	return "", false
}

// procsWithMarker lists the live processes whose environment carries marker (a
// NAME=value pair), as "pid: argv". The detached child's argv does not name
// SATELLE_HOME, but it inherits the hook's environment, so a unique variable set
// only there finds it.
func procsWithMarker(t *testing.T, marker string) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	var found []string
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue // exited, or not ours
		}
		if !slices.Contains(strings.Split(string(env), "\x00"), marker) {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		found = append(found, e.Name()+": "+strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " "))
	}
	return found
}

// waitFor polls cond for up to d.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// releaseHookRun runs a release-stamped SessionStart `hook context` in a fresh
// SATELLE_HOME, with all non-loopback HTTP routed through proxy. dropEnv names
// variables removed from the environment. It returns the home and the marker.
func releaseHookRun(t *testing.T, proxy *releaseProxy, dropEnv ...string) (home, marker string) {
	t.Helper()
	home = t.TempDir()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	marker = "SATELLE_HERMETIC_PROBE=" + hex.EncodeToString(buf)

	var env []string
	for _, kv := range isolatedEnv(t) {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(dropEnv, k) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"SATELLE_HOME="+home,
		marker,
		"HTTPS_PROXY="+proxy.srv.URL, "HTTP_PROXY="+proxy.srv.URL,
		"https_proxy="+proxy.srv.URL, "http_proxy="+proxy.srv.URL,
		"NO_PROXY=", "no_proxy=",
	)

	repo := t.TempDir()
	initCmd := exec.Command(testBin, "init")
	initCmd.Dir, initCmd.Env = repo, env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("satelle init: %v\n%s", err, out)
	}

	// The installed wrapper always passes --harness. Without it this bare payload
	// fingerprints as no harness, whose session_context_event is empty, so the
	// hook returns before it reads or refreshes the release cache.
	hook := exec.Command(testBin, "hook", "context", "--harness", "claude")
	hook.Dir, hook.Env = repo, env
	hook.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"hermetic"}`)
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("satelle hook context: %v\n%s", err, out)
	}
	return home, marker
}

// TestHermeticSessionStartStartsNoRealRefresh (sty_02579374): under the hermetic
// environment a release-stamped SessionStart hook in a fresh SATELLE_HOME starts
// a release check that goes to a closed loopback endpoint, never to GitHub, and
// leaves no process behind; the substrate and policy refreshes do not start in an
// unbound home with no command-policy subscription.
func TestHermeticSessionStartStartsNoRealRefresh(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the surviving-process scan reads /proc")
	}
	if !isReleaseTestBin(t) {
		t.Skip("a dev build never starts a release check")
	}
	for _, name := range []string{"SATELLE_RELEASE_API", "SATELLE_RELEASE_LIST_API", "SATELLE_RELEASE_BASE"} {
		if v := os.Getenv(name); v == "" || strings.Contains(v, "github.com") {
			t.Fatalf("%s = %q: run through scripts/hermetic.sh (make integration), which pins it to a loopback URL", name, v)
		}
	}

	proxy := newReleaseProxy(t)
	home, marker := releaseHookRun(t, proxy)

	// The check was attempted: the stamp only shows that, not where it went.
	var rc struct {
		CheckedAttempt time.Time `json:"checked_attempt"`
	}
	b, err := os.ReadFile(filepath.Join(home, "release-check.json"))
	if err != nil {
		t.Fatalf("release-check.json: %v", err)
	}
	if err := json.Unmarshal(b, &rc); err != nil || rc.CheckedAttempt.IsZero() {
		t.Fatalf("release-check.json = %s (%v), want a checked_attempt: the hook never attempted a check", b, err)
	}

	if !waitFor(5*time.Second, func() bool { return len(procsWithMarker(t, marker)) == 0 }) {
		t.Errorf("processes survive the hook: %v", procsWithMarker(t, marker))
	}
	if s, ok := proxy.sawGitHub(); ok {
		t.Errorf("the hermetic hook reached for GitHub: %s (all: %v)", s, proxy.recorded())
	}

	// Unbound home, no [[gate.command_policy]]: neither refresh may start.
	if m, _ := filepath.Glob(filepath.Join(home, "*", "command-policy.json")); len(m) > 0 {
		t.Errorf("a policy refresh stamp was written: %v", m)
	}
	states, _ := filepath.Glob(filepath.Join(home, "*", "workstate-sync.json"))
	for _, p := range states {
		var st struct {
			Attempt time.Time `json:"substrate_checked_attempt"`
		}
		sb, _ := os.ReadFile(p)
		if json.Unmarshal(sb, &st) == nil && !st.Attempt.IsZero() {
			t.Errorf("%s carries a substrate_checked_attempt: a sync status refresh was started", p)
		}
	}
	for _, p := range procsWithMarker(t, marker) {
		if strings.Contains(p, "sync status") || strings.Contains(p, "policy refresh") {
			t.Errorf("a session-start refresh is still running: %s", p)
		}
	}

	// Positive control: with the release override removed the same hook does
	// reach for GitHub, and the proxy records it. Without this the assertion
	// above could pass for a proxy that sees nothing.
	control := newReleaseProxy(t)
	releaseHookRun(t, control, "SATELLE_RELEASE_API", "SATELLE_RELEASE_LIST_API")
	if !waitFor(5*time.Second, func() bool {
		return slices.Contains(control.recorded(), "CONNECT api.github.com:443")
	}) {
		t.Errorf("positive control: the proxy never recorded CONNECT api.github.com:443 (recorded: %v)", control.recorded())
	}
}
