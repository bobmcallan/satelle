//go:build integration

package tests

import (
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMultiProjectServe drives the real binary in multi-project mode: with two
// registered repos, `satelle serve` becomes a supervisor that spawns one child
// per project on its own port and serves a homepage listing both. Asserts the
// homepage links to each child, and that a child serves its own project page.
func TestMultiProjectServe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home) // isolate the global registry from the real one

	repoA := t.TempDir()
	repoB := t.TempDir()
	mustRun(t, testBin, repoA, "init")
	mustRun(t, testBin, repoB, "init")
	// repoA is the current repo (serve runs there); register repoB so there are
	// two projects and multi-project mode engages.
	mustRun(t, testBin, repoA, "workspace", "add", repoB)

	const port = "8821"
	cmd := exec.Command(testBin, "serve", "--multi", "--port", port, "--no-watch")
	cmd.Dir = repoA
	if err := cmd.Start(); err != nil {
		t.Fatalf("start multi-serve: %v", err)
	}
	t.Cleanup(func() {
		// SIGTERM so the supervisor cancels its context and reaps children.
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
		t.Fatal("multi-serve homepage did not become healthy")
	}

	body := httpGetBody(t, base+"/")
	if !strings.Contains(body, "projects served") {
		t.Errorf("homepage is not the multi-project page:\n%s", body)
	}
	// Two per-port project links, each to a child's #stories page.
	if got := strings.Count(body, `/#stories"`); got < 2 {
		t.Errorf("expected at least 2 project links, got %d:\n%s", got, body)
	}
	childURL := firstChildStoriesURL(body)
	if childURL == "" {
		t.Fatalf("no child project URL found on homepage:\n%s", body)
	}
	// The child actually serves its own project page (single-tenant, its own DB).
	if !waitHealthy(t, strings.TrimSuffix(childURL, "/#stories")+"/healthz", 5*time.Second) {
		t.Errorf("child project %s did not become healthy", childURL)
	}
	childBody := httpGetBody(t, strings.TrimSuffix(childURL, "#stories"))
	if !strings.Contains(childBody, "satelle") || !strings.Contains(childBody, "Stories") {
		t.Errorf("child did not serve a project page:\n%s", childBody)
	}

	// Hot-add: register a THIRD repo while the service runs. The supervisor polls
	// the registry, so the new project must appear on the homepage with no
	// restart (AC3) within a few reconcile cycles.
	repoC := t.TempDir()
	mustRun(t, testBin, repoC, "init")
	mustRun(t, testBin, repoA, "workspace", "add", repoC)

	deadline := time.Now().Add(15 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		if got = strings.Count(httpGetBody(t, base+"/"), `/#stories"`); got >= 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got < 3 {
		t.Errorf("hot-add: homepage still shows %d projects after workspace add (want 3)", got)
	}
}

// httpGetBody fetches a URL and returns the body, failing the test on error.
func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 8192)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

// firstChildStoriesURL extracts the first http://…/#stories link from the
// homepage HTML.
func firstChildStoriesURL(body string) string {
	const marker = `href="http://`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(`href="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	u := rest[:end]
	if !strings.HasSuffix(u, "/#stories") {
		return ""
	}
	return u
}
