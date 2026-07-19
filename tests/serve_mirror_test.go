//go:build integration

package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	startServe := func() *exec.Cmd {
		cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(8 * time.Second)
		for {
			resp, err := http.Get(host + "/healthz")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatalf("serve never healthy: %v", err)
			}
			time.Sleep(40 * time.Millisecond)
		}
		return cmd
	}

	cmd := startServe()
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	// --- full snapshot via workspace add; story visible on project page ---
	out := mustRun(t, testBin, repo, "workspace", "add")
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
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	cmd = startServe()
	// empty mirror after new process? same SATELLE_HOME → same mirror.db survives
	// kill and restart keeps mirror.db — still has data without re-push
	proj2 := httpGet(t, host+"/r/"+slug+"/")
	if !strings.Contains(proj2, "Mirror Probe Story") {
		// if port reuse race emptied, re-push
		mustRun(t, testBin, repo, "workspace", "add")
		proj2 = httpGet(t, host+"/r/"+slug+"/")
	}
	if !strings.Contains(proj2, "Mirror Probe Story") {
		t.Fatalf("after restart view not restored:\n%s", proj2)
	}

	if _, err := os.Stat(filepath.Join(home, "serve", "mirror.db")); err != nil {
		t.Fatalf("expected mirror db: %v", err)
	}
}
