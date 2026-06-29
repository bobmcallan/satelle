//go:build integration

package tests

import (
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMultiProjectServe drives the always-adaptive `serve`: the bound repo is
// served at /, every OTHER registered repo under /<slug>/ (reverse-proxied to a
// child), with a /projects launcher. Asserts the bound repo never moves from /,
// a child is reachable under its slug, and workspace add lands live.
func TestMultiProjectServe(t *testing.T) {
	home := t.TempDir()

	repoA := t.TempDir() // bound (served at /)
	repoB := t.TempDir() // child (served at /<slug>/)
	mustRun(t, testBin, repoA, "init")
	mustRun(t, testBin, repoB, "init")
	// Distinct data per repo so we can prove no cross-project bleed.
	createStory(t, repoA, "AlphaOnlyStory", "")
	createStory(t, repoB, "BetaOnlyStory", "")
	workspaceAdd(t, home, repoA, repoB)

	const port = "8821"
	cmd := exec.Command(testBin, "serve", "--port", port, "--no-watch")
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

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 10*time.Second) {
		t.Fatal("serve did not become healthy")
	}

	// The bound repo is served at the root — its project page, base href "/",
	// showing ONLY its own data (no bleed from repoB).
	root := httpGetBody(t, base+"/")
	if !strings.Contains(root, `<base href="/">`) || !strings.Contains(root, "AlphaOnlyStory") {
		t.Errorf("root is not the bound project page with its own data:\n%s", root)
	}
	if strings.Contains(root, "BetaOnlyStory") {
		t.Error("bound repo page leaked the child repo's story (data bleed)")
	}

	// An unknown /<prefix>/ is not a registered project → 404 via the bound mux (AC3).
	if code := httpStatus(t, base+"/bogus/fragment/stories"); code != http.StatusNotFound {
		t.Errorf("unknown project prefix = %d, want 404", code)
	}

	// /projects lists the bound repo + the child (≥2 #stories links).
	proj := httpGetBody(t, base+"/projects")
	if !strings.Contains(proj, "projects served") {
		t.Errorf("/projects is not the launcher:\n%s", proj)
	}
	childPath := firstChildPath(proj)
	if childPath == "" {
		t.Fatalf("no child /<slug>/ link on /projects:\n%s", proj)
	}

	// The child is reachable under its slug (proxied), with its own base href so
	// its assets/SSE resolve under the prefix, and shows ONLY its own data.
	childBody := httpGetBody(t, base+childPath)
	if !strings.Contains(childBody, `<base href="`+childPath+`">`) || !strings.Contains(childBody, "BetaOnlyStory") {
		t.Errorf("child at %s did not serve its own prefixed project page:\n%s", childPath, childBody)
	}
	if strings.Contains(childBody, "AlphaOnlyStory") {
		t.Errorf("child repo page leaked the bound repo's story (data bleed)")
	}

	// Hot-add: register a THIRD repo while running; it must appear on /projects.
	repoC := t.TempDir()
	mustRun(t, testBin, repoC, "init")
	workspaceAdd(t, home, repoA, repoC)
	deadline := time.Now().Add(15 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		if got = strings.Count(httpGetBody(t, base+"/projects"), `/#stories"`); got >= 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got < 3 {
		t.Errorf("hot-add: /projects shows %d projects after workspace add (want 3)", got)
	}
}

// workspaceAdd runs `workspace add` in dir with an isolated SATELLE_HOME.
func workspaceAdd(t *testing.T, home, dir, repo string) {
	t.Helper()
	cmd := exec.Command(testBin, "workspace", "add", repo)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
}

var childPathRe = regexp.MustCompile(`href="(/[^"/]+/)#stories"`)

// firstChildPath returns the first /<slug>/ launcher link that is not the root.
func firstChildPath(body string) string {
	for _, m := range childPathRe.FindAllStringSubmatch(body, -1) {
		if m[1] != "/" {
			return m[1]
		}
	}
	return ""
}

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
