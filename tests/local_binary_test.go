//go:build integration

package tests

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	// Strip SATELLE_LOCAL_EXEC so a parent/CI process that set the loop guard
	// cannot suppress re-exec and flake this case.
	cmd := exec.Command(testBin, "version")
	cmd.Dir = sub
	cmd.Env = envWithoutLocalExecMarker()
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
	cmd.Env = append(envWithoutLocalExecMarker(), "SATELLE_LOCAL_EXEC=1")
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
// TestPinnedServePushFedIsolation: a repo-local pin runs serve; only pushed
// partitions appear. A workspace-registered other repo is not auto-mirrored
// (push-fed, not workspace aggregate). Port is the configured/default serve port.
func TestPinnedServePushFedIsolation(t *testing.T) {
	home := isolatedHome(t)
	repo := t.TempDir()
	other := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, other, "init")
	createStory(t, repo, "ThisRepoStory", "")
	createStory(t, other, "OtherRepoStory", "")
	workspaceAdd(t, home, repo, other)

	pin := filepath.Join(repo, ".satelle", "satelle")
	binBytes, err := os.ReadFile(testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	port := freeListenPort(t)
	env := append(os.Environ(), "SATELLE_HOME="+home)
	// pin is a byte-copy of testBin; StartServe still owns lifetime + process group.
	h := StartServeHealthy(t, pin, repo, env, 10*time.Second, "--port", port, "--no-watch")
	base := h.Base

	localBody := fmt.Sprintf("[review]\ngate_create = false\n\n[server]\nendpoint = %q\n", base)
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(localBody), 0o644); err != nil {
		t.Fatal(err)
	}
	seedWorkspaceAdd(t, testBin, repo, base)

	root := httpGetBody(t, base+"/")
	slug := filepath.Base(repo)
	otherSlug := filepath.Base(other)
	if !strings.Contains(root, "/r/"+slug+"/") {
		t.Errorf("landing should list this repo (%s):\n%s", slug, root)
	}
	if strings.Contains(root, "/r/"+otherSlug+"/") || strings.Contains(root, otherSlug) {
		t.Errorf("must NOT list workspace-added repo without push (%s):\n%s", otherSlug, root)
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

// envWithoutLocalExecMarker is os.Environ without SATELLE_LOCAL_EXEC so a
// parent process that set the re-exec loop guard cannot suppress pin dispatch.
func envWithoutLocalExecMarker() []string {
	const key = "SATELLE_LOCAL_EXEC="
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, key) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TestVersionReportsBinaryScope checks the version line names which install is
// active (sty_fc1163dd): the repo-local pin reports "repo-local pin: <path>",
// the global build artifact reports "global".
func TestVersionReportsBinaryScope(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	pin := filepath.Join(repo, ".satelle", "satelle")
	b, err := os.ReadFile(testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, b, 0o755); err != nil {
		t.Fatal(err)
	}

	// Running the pin reports the repo-local scope and its path.
	out := mustRun(t, pin, repo, "version")
	if !strings.Contains(out, "repo-local pin") || !strings.Contains(out, pin) {
		t.Errorf("pin version should report repo-local pin and its path, got:\n%s", out)
	}

	// The global build artifact (not under a .satelle/) reports global.
	bare := t.TempDir()
	out = mustRun(t, testBin, bare, "version")
	if !strings.Contains(out, "global") || strings.Contains(out, "repo-local pin") {
		t.Errorf("global binary version should report global, got:\n%s", out)
	}
}
