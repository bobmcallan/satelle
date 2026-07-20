//go:build integration

package tests

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWebLoginMirrorReadOnlyAffordance: on the push-fed mirror there is no
// hosted sign-in form (order:4). The landing is the workspace; identity comes
// only from the pushed meta blob when present.
func TestWebLoginMirrorReadOnlyAffordance(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

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
	cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "SATELLE_HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	if !waitHealthy(t, host+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	seedWorkspaceAdd(t, testBin, repo, host)

	// Landing (workspace) — no oauth/login affordance.
	landing := httpGet(t, host+"/")
	if strings.Contains(landing, `href="oauth/login"`) || strings.Contains(landing, "account-menu") {
		t.Fatalf("mirror landing must not render hosted auth controls:\n%s", landing)
	}
	if !strings.Contains(landing, "workspace") {
		t.Fatalf("landing missing workspace chrome:\n%s", landing)
	}

	// Project page similarly RO — no sign-in form.
	slug := filepath.Base(repo)
	proj := httpGet(t, host+"/r/"+slug+"/")
	if strings.Contains(proj, `href="oauth/login"`) || strings.Contains(proj, "account-menu") {
		t.Fatalf("mirror project page must not render hosted auth controls:\n%s", proj)
	}
	// oauth routes are not registered on the mirror.
	if code := httpStatus(t, host+"/oauth/login"); code == 200 {
		// Accept 404/405/etc — must not be a working login page.
		t.Fatalf("/oauth/login should not serve a login flow on the mirror, got %d", code)
	}
}
