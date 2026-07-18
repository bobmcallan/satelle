//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeMirrorPushFed proves sty_dbdadfa0 + sty_1dde0d47 core:
// serve never needs a repo store annotation, accepts snapshot via ui push,
// and renders partition landing from the push-fed mirror.
func TestServeMirrorPushFed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init", "--harness", "claude")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	local := filepath.Join(repo, ".satelle", "satelle.local.toml")
	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = \"http://127.0.0.1:%d\"\n", port)
	if err := os.WriteFile(local, []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "story", "create",
		"--title", "Mirror Probe",
		"--body", "visible in push-fed UI",
		"--acceptance", "1. listed",
		"--category", "chore",
	)

	home := t.TempDir()
	cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	deadline := time.Now().Add(8 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve never became healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	out := mustRun(t, testBin, repo, "ui", "push")
	if !strings.Contains(out, "ui push: ok") {
		t.Fatalf("ui push: %s", out)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "/r/") {
		t.Fatalf("landing missing partition link:\n%s", body)
	}
	if !strings.Contains(string(body), "push-fed") {
		t.Fatalf("landing missing push-fed marker:\n%s", body)
	}

	// ingest change (order:2 path)
	ev, _ := json.Marshal(map[string]string{
		"repo_key": "rk-test", "topic": "stories", "entity": "story",
		"at": time.Now().UTC().Format(time.RFC3339),
	})
	resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%d/ingest/change", port), "application/json", strings.NewReader(string(ev)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("ingest change status %d", resp.StatusCode)
	}

	// Mirror DB lives under serve/, not a per-repo satelle.db opened by serve.
	if _, err := os.Stat(filepath.Join(home, "serve", "mirror.db")); err != nil {
		t.Fatalf("expected mirror db under serve/: %v", err)
	}
}
