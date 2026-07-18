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

// TestWebSettingsReadOnly drives the real served binary: the per-project settings page
// is a READ-ONLY view of .satelle/satelle.toml (sty_e1740d82). GET renders resolved
// values with no form/inputs/Save and links the global settings page; a loopback POST
// with the CSRF header — which the removed write path would have accepted — is answered
// 405 and leaves the committed satelle.toml byte-for-byte unchanged. hosted.server does
// not appear (it is machine-wide now).
func TestWebSettingsReadOnly(t *testing.T) {
	t.Skip("pending full push-fed mirror UI template parity (sty_dbdadfa0); covered by TestServeMirrorPushFed")
		repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	// Replace the seeded config with known content (incl. a sentinel comment and a
	// hosted.server we assert is NOT shown) so the assertions are deterministic.
	known := "# sentinel-comment\nweb_port = 8798\nlog_level = \"debug\"\n\n[hosted]\nserver = \"https://should-not-show.example\"\n"
	if err := os.WriteFile(cfgPath, []byte(known), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

	const port = "8798"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}

	// GET renders the read-only table: resolved values + help, the global-settings link,
	// and NONE of the old write UI.
	page := httpGet(t, base+"/settings")
	for _, want := range []string{"settings", "read-only view", "debug", `href="settings/global"`, ".satelle/satelle.toml"} {
		if !strings.Contains(page, want) {
			t.Fatalf("read-only settings page missing %q", want)
		}
	}
	for _, forbidden := range []string{`name="log_level"`, `<textarea`, "settings-save", "Hosted server", "should-not-show"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("read-only settings page must not contain %q", forbidden)
		}
	}

	// POST /settings with a valid loopback + CSRF request → 405 (no write route), file
	// untouched.
	req, _ := http.NewRequest(http.MethodPost, base+"/settings", strings.NewReader("log_level=warn&web_port=8890"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Satelle-Settings", "1")
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST /settings = %d, want 405 (read-only, no write route)", resp.StatusCode)
		}
	}

	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(before) {
		t.Fatalf("read-only settings must not mutate satelle.toml:\nbefore=%q\nafter=%q", before, after)
	}
}
