//go:build integration

package tests

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMultiProjectServe drives the always-adaptive `serve`: the root (/) is a
// connected-projects landing and EVERY registered repo — including the repo
// serve was launched from — is served under /<slug>/ (reverse-proxied to a
// child). Asserts the landing lists every project (counts, not story titles),
// each project is reachable and isolated under its own slug, /projects redirects
// to the landing, and a workspace add lands live.
func TestMultiProjectServe(t *testing.T) {
	home := t.TempDir()

	repoA := t.TempDir() // launch repo — now served under its own /<slug>/ too
	repoB := t.TempDir() // another registered project
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

	// Tempdir basenames are numeric (001, 002, …), so each slug is its basename.
	slugA := filepath.Base(repoA)
	slugB := filepath.Base(repoB)

	// / is the connected-projects landing — a launcher, not any single repo's
	// project page. It lists every project (uniform, no "launched here" badge)
	// with counts (not story titles), plus the add-a-project help.
	root := httpGetBody(t, base+"/")
	for _, want := range []string{
		"connected project",
		"satelle workspace add",
		"satelle update",
		`href="/` + slugA + `/#stories"`,
		`href="/` + slugB + `/#stories"`,
	} {
		if !strings.Contains(root, want) {
			t.Errorf("landing missing %q:\n%s", want, root)
		}
	}
	if strings.Contains(root, "launched here") {
		t.Errorf("landing still renders the retired 'launched here' badge:\n%s", root)
	}
	if strings.Contains(root, "AlphaOnlyStory") || strings.Contains(root, "BetaOnlyStory") {
		t.Errorf("landing leaked a project's story titles (it should show counts only):\n%s", root)
	}

	// /projects redirects to the landing at / (back-compat for older links).
	if loc, code := httpRedirect(t, base+"/projects"); code != http.StatusFound || loc != "/" {
		t.Errorf("/projects = %d -> %q, want 302 -> /", code, loc)
	}

	// The launch repo is now served under its OWN slug — prefixed base href so its
	// assets/SSE resolve under the prefix — and shows ONLY its own data.
	aBody := httpGetBody(t, base+"/"+slugA+"/")
	if !strings.Contains(aBody, `<base href="/`+slugA+`/">`) || !strings.Contains(aBody, "AlphaOnlyStory") {
		t.Errorf("launch repo not served at /%s/ with its own data:\n%s", slugA, aBody)
	}
	if strings.Contains(aBody, "BetaOnlyStory") {
		t.Error("launch repo page leaked the other repo's story (data bleed)")
	}

	// The other project under its slug, with its own base href and ONLY its data.
	bBody := httpGetBody(t, base+"/"+slugB+"/")
	if !strings.Contains(bBody, `<base href="/`+slugB+`/">`) || !strings.Contains(bBody, "BetaOnlyStory") {
		t.Errorf("project not served at /%s/ with its own data:\n%s", slugB, bBody)
	}
	if strings.Contains(bBody, "AlphaOnlyStory") {
		t.Error("project page leaked the launch repo's story (data bleed)")
	}

	// An unknown /<prefix>/ is not a registered project → 404 via shared chrome.
	if code := httpStatus(t, base+"/bogus/fragment/stories"); code != http.StatusNotFound {
		t.Errorf("unknown project prefix = %d, want 404", code)
	}

	// Hot-add: register a THIRD repo while running; it must appear on the landing.
	repoC := t.TempDir()
	mustRun(t, testBin, repoC, "init")
	workspaceAdd(t, home, repoA, repoC)
	deadline := time.Now().Add(15 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		if got = strings.Count(httpGetBody(t, base+"/"), `/#stories"`); got >= 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got < 3 {
		t.Errorf("hot-add: landing shows %d projects after workspace add (want 3)", got)
	}
}

// TestFooterConsistentAcrossPages asserts the one shared footer (satelle
// <version>) renders identically on the landing, a project page, /help and
// /workspace — the footer is one template, not a per-page copy.
func TestFooterConsistentAcrossPages(t *testing.T) {
	base, repo := serveRepo(t, "8823") // base is host+/<slug> (the project page)
	host := strings.TrimSuffix(base, "/"+filepath.Base(repo))

	footer := func(url string) string {
		m := footerRe.FindStringSubmatch(httpGetBody(t, url))
		if m == nil {
			t.Fatalf("no site-footer on %s", url)
		}
		return m[1]
	}

	want := footer(base + "/") // the project page footer
	if !strings.HasPrefix(want, "satelle ") {
		t.Errorf("footer is not 'satelle <version>': %q", want)
	}
	for _, url := range []string{host + "/", host + "/help", host + "/workspace"} {
		if got := footer(url); got != want {
			t.Errorf("footer on %s = %q, want %q (footers must match)", url, got, want)
		}
	}
}

var footerRe = regexp.MustCompile(`<span class="footer-version">([^<]*)</span>`)

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

// httpRedirect issues a GET that does NOT follow redirects, returning the
// Location header and status code.
func httpRedirect(t *testing.T, url string) (string, int) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.Header.Get("Location"), resp.StatusCode
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
