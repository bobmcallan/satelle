//go:build integration

package tests

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLocalBinaryReexec drives the real binary's repo-local precedence
// (sty_fe3ee313): with a .satelle/satelle pin present, the globally-invoked
// satelle re-execs the pin; the loop-guard env marker suppresses that
// (so the in-process binary runs). The pin is a tiny script that prints a
// recognisable marker, so the test can tell which binary actually ran.
func TestLocalBinaryReexec(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(repo, ".satelle", "satelle")
	if err := os.WriteFile(pin, []byte("#!/bin/sh\necho LOCAL-PIN-RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// From inside the repo, the global binary must re-exec the pin.
	cmd := exec.Command(testBin, "version")
	cmd.Dir = sub
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version (should re-exec pin): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LOCAL-PIN-RAN") {
		t.Errorf("expected the repo-local pin to run, got:\n%s", out)
	}

	// With the loop-guard marker set, the in-process binary runs (no re-exec).
	cmd = exec.Command(testBin, "version")
	cmd.Dir = sub
	cmd.Env = append(os.Environ(), "SATELLE_LOCAL_EXEC=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version (guard set): %v\n%s", err, out)
	}
	if strings.Contains(string(out), "LOCAL-PIN-RAN") {
		t.Errorf("loop guard should suppress re-exec, but the pin ran:\n%s", out)
	}
	if !strings.Contains(string(out), "satelle ") {
		t.Errorf("expected the real satelle version line, got:\n%s", out)
	}
}

// TestLocalModeServeSingleProjectOwnPort drives the repo-local pin's `serve`
// (sty_6b07cfb1): running as <repo>/.satelle/satelle it must (a) listen on a
// deterministic per-repo port in the local range (never 8787) and (b) show only
// THIS project, even though another repo is registered in the global workspace —
// local mode does not aggregate. Global mode (covered by TestMultiProjectServe)
// would list both.
func TestLocalModeServeSingleProjectOwnPort(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	other := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, other, "init")
	createStory(t, repo, "ThisRepoStory", "")
	createStory(t, other, "OtherRepoStory", "")
	workspaceAdd(t, home, repo, other) // register `other` in the global workspace

	// Install the pin: copy the test binary to <repo>/.satelle/satelle.
	pin := filepath.Join(repo, ".satelle", "satelle")
	binBytes, err := os.ReadFile(testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// Run the PIN's serve with no --port: local mode must pick the deterministic
	// per-repo port itself.
	cmd := exec.Command(pin, "serve", "--no-watch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pin serve: %v", err)
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

	port := scanServePort(t, stdout, 10*time.Second)
	if port == 8787 {
		t.Fatal("local mode must NOT serve on the global default port 8787")
	}
	if port < 8800 || port >= 9000 {
		t.Errorf("local port %d not in the deterministic range [8800,9000)", port)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if !waitHealthy(t, base+"/healthz", 10*time.Second) {
		t.Fatal("local-mode serve did not become healthy")
	}

	root := httpGetBody(t, base+"/")
	slug := filepath.Base(repo)
	otherSlug := filepath.Base(other)
	if !strings.Contains(root, "/"+slug+"/") {
		t.Errorf("landing should list this repo (%s):\n%s", slug, root)
	}
	if strings.Contains(root, "/"+otherSlug+"/") {
		t.Errorf("local mode must NOT aggregate the workspace-added repo (%s):\n%s", otherSlug, root)
	}
}

// scanServePort reads the serve banner from r until it finds the listen port
// (http://127.0.0.1:<port>/) or times out.
func scanServePort(t *testing.T, r interface{ Read([]byte) (int, error) }, timeout time.Duration) int {
	t.Helper()
	re := regexp.MustCompile(`http://127\.0\.0\.1:(\d+)/`)
	found := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				p, _ := strconv.Atoi(m[1])
				found <- p
				return
			}
		}
	}()
	select {
	case p := <-found:
		return p
	case <-time.After(timeout):
		t.Fatal("did not find the serve port in the banner output")
		return 0
	}
}
