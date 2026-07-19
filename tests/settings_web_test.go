//go:build integration

package tests

import (
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

// TestWebSettingsReadOnly drives the push-fed serve: project settings are a
// READ-ONLY view of the pushed settings blob (sty_e1740d82 + epic:mirror-ui-parity).
// GET /r/{slug}/settings renders resolved values with no form/inputs/Save; global
// settings are omitted on the mirror; non-ingest POSTs stay rejected.
func TestWebSettingsReadOnly(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	known := "# sentinel-comment\nweb_port = 8798\nlog_level = \"debug\"\n\n[hosted]\nserver = \"https://should-not-show.example\"\n"
	if err := os.WriteFile(cfgPath, []byte(known), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

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
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	if !waitHealthy(t, host+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	mustRun(t, testBin, repo, "ui", "push")

	slug := filepath.Base(repo)
	page := httpGet(t, host+"/r/"+slug+"/settings")
	for _, want := range []string{"settings", "read-only view", "debug", ".satelle/satelle.toml"} {
		if !strings.Contains(page, want) {
			t.Fatalf("read-only settings page missing %q:\n%s", want, page)
		}
	}
	// Mirror RO: no global-settings link, no write UI, hosted.server not shown.
	for _, forbidden := range []string{`name="log_level"`, `<textarea`, "settings-save", "settings/global", "should-not-show"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("read-only settings page must not contain %q", forbidden)
		}
	}

	// POST /settings (root or project) → not an OK mutation path.
	for _, path := range []string{"/settings", "/r/" + slug + "/settings"} {
		req, _ := http.NewRequest(http.MethodPost, host+path, strings.NewReader("log_level=warn&web_port=8890"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Satelle-Settings", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			t.Fatalf("POST %s must not succeed on push-fed serve, got %d", path, resp.StatusCode)
		}
	}

	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(before) {
		t.Fatalf("read-only settings must not mutate satelle.toml:\nbefore=%q\nafter=%q", before, after)
	}
}
