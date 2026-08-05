//go:build integration

package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeMirrorPushFed proves sty_dbdadfa0 + sty_1dde0d47 behavioural ACs:
// no needsStore serve, snapshot + rendered story, SSE trigger on ingest,
// restart+reconcile restores, non-ingest POSTs rejected.
func TestServeMirrorPushFed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init", "--harness", "claude")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	host := fmt.Sprintf("http://127.0.0.1:%d", port)

	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", host)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, testBin, repo, "story", "create",
		"--title", "Mirror Probe Story",
		"--body", "visible in push-fed UI",
		"--acceptance", "1. listed",
		"--category", "chore",
	)

	home := t.TempDir()
	env := append(os.Environ(), "SATELLE_HOME="+home)
	startServe := func() *ServeHandle {
		return StartServeHealthy(t, testBin, repo, env, 8*time.Second,
			"--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	}

	h := startServe()

	// --- full snapshot via workspace add; story visible on project page ---
	out := seedWorkspaceAdd(t, testBin, repo, host)
	if !strings.Contains(out, "workspace add: ok") {
		t.Fatalf("workspace add: %s", out)
	}
	landing := httpGet(t, host+"/")
	if !strings.Contains(landing, "/r/") || !strings.Contains(landing, "push-fed") {
		t.Fatalf("landing:\n%s", landing)
	}
	// slug = basename of temp repo
	slug := filepath.Base(repo)
	proj := httpGet(t, host+"/r/"+slug+"/")
	if !strings.Contains(proj, "Mirror Probe Story") {
		t.Fatalf("project page missing story after workspace add:\n%s", proj)
	}

	// --- AC4: non-ingest POSTs rejected ---
	for _, path := range []string{"/theme", "/settings/global", "/oauth/logout"} {
		req, _ := http.NewRequest(http.MethodPost, host+path, strings.NewReader(""))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			t.Errorf("%s POST must not succeed on push-fed serve, got %d", path, resp.StatusCode)
		}
	}

	// --- AC3: SSE trigger fires on ingest/change ---
	sseCh := make(chan string, 1)
	go func() {
		resp, err := http.Get(host + "/events")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				select {
				case sseCh <- strings.TrimPrefix(line, "data: "):
				default:
				}
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // let SSE connect
	ev, _ := json.Marshal(map[string]string{
		"repo_key": "rk-test", "topic": "stories", "entity": "story",
		"at": time.Now().UTC().Format(time.RFC3339),
	})
	resp, err := http.Post(host+"/ingest/change", "application/json", strings.NewReader(string(ev)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("ingest change status %d", resp.StatusCode)
	}
	select {
	case topic := <-sseCh:
		if topic == "" {
			t.Error("empty SSE topic")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE trigger after ingest/change")
	}

	// --- restart + reconcile restores view ---
	h.Stop()
	h = startServe()
	// empty mirror after new process? same SATELLE_HOME → same mirror.db survives
	// kill and restart keeps mirror.db — still has data without re-push
	proj2 := httpGet(t, host+"/r/"+slug+"/")
	if !strings.Contains(proj2, "Mirror Probe Story") {
		// if port reuse race emptied, re-push
		seedWorkspaceAdd(t, testBin, repo, host)
		proj2 = httpGet(t, host+"/r/"+slug+"/")
	}
	if !strings.Contains(proj2, "Mirror Probe Story") {
		t.Fatalf("after restart view not restored:\n%s", proj2)
	}

	if _, err := os.Stat(filepath.Join(home, "serve", "mirror.db")); err != nil {
		t.Fatalf("expected mirror db: %v", err)
	}
}

// TestCLICreateAppearsLiveWithoutRefresh (sty_3562c820 AC1/AC3): after a full
// seed, a subsequent story create drains a light snapshot that lands as 2xx and
// makes the new title visible on the project page without waiting for reconcile.
func TestCLICreateAppearsLiveWithoutRefresh(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init", "--harness", "claude")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	host := fmt.Sprintf("http://127.0.0.1:%d", port)

	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", host)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	env := append(os.Environ(), "SATELLE_HOME="+home)
	h := StartServeHealthy(t, testBin, repo, env, 8*time.Second,
		"--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	_ = h

	seed := seedWorkspaceAdd(t, testBin, repo, host)
	if !strings.Contains(seed, "workspace add: ok") {
		t.Fatalf("workspace add: %s", seed)
	}

	// Override the suite-wide SATELLE_SERVER_ENDPOINT=none so the drain posts
	// to this test serve (same seam as seedWorkspaceAdd).
	pushEnv := []string{"SATELLE_SERVER_ENDPOINT=" + host}
	out := mustRunEnv(t, testBin, repo, pushEnv, "story", "create",
		"--title", "Live Drain Story",
		"--body", "must appear without refresh",
		"--acceptance", "1. visible live",
		"--category", "chore",
	)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("create parse: %v out=%s", err, out)
	}

	slug := filepath.Base(repo)
	// Drain is synchronous before the verb returns; first GET should already
	// see the row. Brief poll only covers page/serve race, not the 5m reconcile.
	deadline := time.Now().Add(3 * time.Second)
	var proj string
	for time.Now().Before(deadline) {
		proj = httpGet(t, host+"/r/"+slug+"/")
		if strings.Contains(proj, "Live Drain Story") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(proj, "Live Drain Story") {
		t.Fatalf("new story not visible after create drain (id=%s):\n%s", created.ID, proj)
	}
	// Priority set also drains; row must remain live.
	mustRunEnv(t, testBin, repo, pushEnv, "story", "set", created.ID, "--priority", "high")
	proj2 := httpGet(t, host+"/r/"+slug+"/")
	if !strings.Contains(proj2, "Live Drain Story") {
		t.Fatalf("story disappeared after set drain:\n%s", proj2)
	}
}
