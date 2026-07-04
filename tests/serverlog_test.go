//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeWritesServerLog drives a real `satelle serve` and confirms the request
// logging middleware (sty_07cec95f) writes .satelle/logs/server.log through the
// cmd_serve.go wiring — the leg the internal/web unit tests cannot reach. It asserts
// the log exists and carries the tab-separated INFO line for a served request plus a
// 404 for an unknown path.
func TestServeWritesServerLog(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	const port = "8795"
	cmd := exec.Command(testBin, "serve", "--port", port)
	cmd.Dir = repo
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}
	httpGet(t, base+"/")
	if code := httpStatus(t, base+"/nope"); code != 404 {
		t.Fatalf("/nope = %d, want 404", code)
	}

	// The middleware appends best-effort after the response, so poll briefly.
	logPath := filepath.Join(repo, ".satelle", "logs", "server.log")
	var log string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logPath); err == nil {
			log = string(b)
			if strings.Contains(log, "\t/nope\t404\t") {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if log == "" {
		t.Fatalf("server did not write %s", logPath)
	}
	if !strings.Contains(log, "\tINFO\tGET\t/healthz\t200\t") {
		t.Errorf("server.log missing the /healthz INFO line:\n%s", log)
	}
	if !strings.Contains(log, "\t/nope\t404\t") {
		t.Errorf("server.log missing the 404 line for /nope:\n%s", log)
	}
}
