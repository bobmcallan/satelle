//go:build integration

package tests

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWebSettingsEditor drives the real served binary: the settings page renders
// the config table, a loopback POST with the CSRF header writes satelle.toml
// (comment preserved), and a POST without the header is rejected 403 (sty_ffe53865).
func TestWebSettingsEditor(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	// Seed a comment we can assert survives the write.
	orig, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, append([]byte("# sentinel-comment\n"), orig...), 0o644); err != nil {
		t.Fatal(err)
	}

	const port = "8798"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
	cmd.Dir = repo
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}

	// GET renders the config table.
	page := httpGet(t, base+"/settings")
	for _, want := range []string{"satelle", "settings", `name="log_level"`, "Hosted server"} {
		if !strings.Contains(page, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}

	// POST without the CSRF header → 403, file untouched.
	noHdr, _ := http.NewRequest(http.MethodPost, base+"/settings", strings.NewReader("log_level=warn"))
	noHdr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := http.DefaultClient.Do(noHdr); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST without CSRF header = %d, want 403", resp.StatusCode)
		}
	}

	// POST with the CSRF header from loopback → accepted; the value lands and the
	// comment survives.
	withHdr, _ := http.NewRequest(http.MethodPost, base+"/settings", strings.NewReader("log_level=warn&web_port=8890"))
	withHdr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withHdr.Header.Set("X-Satelle-Settings", "1")
	if resp, err := http.DefaultClient.Do(withHdr); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			t.Fatalf("POST with CSRF header = %d, want < 400", resp.StatusCode)
		}
	}

	saved, _ := os.ReadFile(cfgPath)
	s := string(saved)
	if !strings.Contains(s, "# sentinel-comment") {
		t.Fatalf("comment not preserved after write:\n%s", s)
	}
	if !strings.Contains(s, `log_level = "warn"`) || !strings.Contains(s, "web_port = 8890") {
		t.Fatalf("edits not written:\n%s", s)
	}
	// A subsequent GET reflects the saved values.
	page2 := httpGet(t, base+"/settings")
	if !strings.Contains(page2, `value="warn"`) || !strings.Contains(page2, `value="8890"`) {
		t.Fatalf("GET did not reflect saved values")
	}
}
