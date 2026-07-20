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
	"syscall"
	"testing"
	"time"
)

// TestMultiPartitionMirrorServe proves the push-fed mirror holds multiple
// partitions (decision-local-db-placement): workspace landing lists each
// partition with counts; each /r/{slug}/ is isolated; no reverse-proxy children.
func TestMultiPartitionMirrorServe(t *testing.T) {
	home := isolatedHome(t)

	repoA := t.TempDir()
	repoB := t.TempDir()
	mustRun(t, testBin, repoA, "init")
	mustRun(t, testBin, repoB, "init")
	createStory(t, repoA, "AlphaOnlyStory", "")
	createStory(t, repoB, "BetaOnlyStory", "")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	host := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Point both repos at the same serve endpoint.
	for _, repo := range []string{repoA, repoB} {
		localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", host)
		if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(testBin, "serve", "--addr", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = repoA
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
	if !waitHealthy(t, host+"/healthz", 10*time.Second) {
		t.Fatal("serve did not become healthy")
	}

	seedWorkspaceAdd(t, testBin, repoA, host)
	seedWorkspaceAdd(t, testBin, repoB, host)

	slugA := filepath.Base(repoA)
	slugB := filepath.Base(repoB)

	root := httpGetBody(t, host+"/")
	for _, want := range []string{
		"workspace",
		"push-fed",
		`href="/r/` + slugA + `/"`,
		`href="/r/` + slugB + `/"`,
	} {
		if !strings.Contains(root, want) {
			t.Errorf("landing missing %q:\n%s", want, root)
		}
	}

	// /workspace aliases the landing.
	ws := httpGetBody(t, host+"/workspace")
	if !strings.Contains(ws, "workspace") {
		t.Errorf("/workspace should render landing:\n%s", ws)
	}

	aBody := httpGetBody(t, host+"/r/"+slugA+"/")
	if !strings.Contains(aBody, "AlphaOnlyStory") {
		t.Errorf("repo A project page missing its story:\n%s", aBody)
	}
	if strings.Contains(aBody, "BetaOnlyStory") {
		t.Error("repo A page leaked B's story")
	}
	if !strings.Contains(aBody, `<base href="/r/`+slugA+`/">`) {
		t.Errorf("expected project base href under /r/%s/:\n%s", slugA, aBody)
	}

	bBody := httpGetBody(t, host+"/r/"+slugB+"/")
	if !strings.Contains(bBody, "BetaOnlyStory") {
		t.Errorf("repo B project page missing its story:\n%s", bBody)
	}
	if strings.Contains(bBody, "AlphaOnlyStory") {
		t.Error("repo B page leaked A's story")
	}

	// Non-ingest POST still rejected.
	req, _ := http.NewRequest(http.MethodPost, host+"/theme", strings.NewReader("theme=dark"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		t.Errorf("POST /theme must not succeed on mirror, got %d", resp.StatusCode)
	}
}

func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	return httpGet(t, url)
}

// workspaceAdd registers repo under home's workspace from dir as cwd.
// When endpoint is non-empty it is passed as SATELLE_SERVER_ENDPOINT so seed
// targets the test serve (overrides suite-wide none from isolatedEnv).
func workspaceAdd(t *testing.T, home, dir, repo string) {
	t.Helper()
	workspaceAddEndpoint(t, home, dir, repo, "")
}

func workspaceAddEndpoint(t *testing.T, home, dir, repo, endpoint string) {
	t.Helper()
	cmd := exec.Command(testBin, "workspace", "add", repo)
	cmd.Dir = dir
	env := append(os.Environ(), "SATELLE_HOME="+home)
	if endpoint != "" {
		env = append(env, "SATELLE_SERVER_ENDPOINT="+endpoint)
	} else {
		// No intentional seed — keep discovery off (sty_5aa08259).
		env = append(env, "SATELLE_SERVER_ENDPOINT=none")
	}
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
}
