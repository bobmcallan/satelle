//go:build integration

package tests

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProxiedSSEAbortDoesNotPoisonConnection drives the REAL serve process: a
// proxied child SSE stream (/<slug>/events) is aborted mid-stream, exercising the
// supervisor's httputil.ReverseProxy path that panics http.ErrAbortHandler. The
// fix (sty_fa24469a) re-panics it so the server closes the connection cleanly —
// so no bogus "panic: net/http: abort Handler" ERROR line is logged, and a
// subsequent / on the same client still responds promptly (pre-fix it hung).
func TestProxiedSSEAbortDoesNotPoisonConnection(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	workspaceAdd(t, home, repo, repo)

	const port = "8822"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 10*time.Second) {
		t.Fatal("serve did not become healthy")
	}
	slug := filepath.Base(repo)

	// Open the proxied child SSE, read the stream open, then abort mid-stream by
	// cancelling the request — the supervisor's reverse proxy raises
	// http.ErrAbortHandler on the broken copy.
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+slug+"/events", nil)
	if resp, err := client.Do(req); err == nil {
		buf := make([]byte, 16)
		_, _ = resp.Body.Read(buf) // the ": connected" stream open
		cancel()                   // disconnect mid-stream
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	} else {
		cancel()
	}

	// Give the supervisor a moment to unwind the aborted proxy handler.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if httpStatus(t, base+"/healthz") == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The aborted stream must NOT have logged a panic ERROR line.
	logPath := filepath.Join(repo, ".satelle", "logs", "server.log")
	if b, err := os.ReadFile(logPath); err == nil {
		if strings.Contains(string(b), "abort Handler") {
			t.Fatalf("aborted proxied SSE logged a panic ERROR line (ErrAbortHandler not re-panicked):\n%s", b)
		}
	}

	// A subsequent / on the same client must still respond promptly.
	start := time.Now()
	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET / after an aborted proxied stream failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("GET / took %v after an aborted stream — connection poisoned", elapsed)
	}
}
