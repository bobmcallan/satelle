//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeWritesServerLog drives a real `satelle serve` and confirms the request
// logging middleware writes the serve-wide server.log under the home-keyed mirror
// plane (~/…/serve/server.log).
func TestServeWritesServerLog(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	home := isolatedHome(t)
	const port = "8795"
	env := append(os.Environ(), "SATELLE_HOME="+home)
	h := StartServeHealthy(t, testBin, repo, env, 5*time.Second, "--port", port)
	base := h.Base
	httpGet(t, base+"/")
	if code := httpStatus(t, base+"/nope"); code != 404 {
		t.Fatalf("/nope = %d, want 404", code)
	}

	logPath := filepath.Join(home, "serve", "server.log")
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
