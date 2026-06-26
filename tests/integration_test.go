//go:build integration

// Package tests holds satelle's black-box integration tests: they build the
// real binary and drive it end-to-end through the dogfood flow (init → story →
// index → status → serve), asserting on actual process output. Gated behind the
// `integration` build tag so the default `go test ./...` stays fast and
// hermetic; run with:
//
//	go test -tags integration ./tests/...
package tests

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildBinary compiles satelle once into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "satelle")
	// The test runs from tests/, so the module root is one level up.
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/satelle")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build satelle: %v\n%s", err, out)
	}
	return bin
}

// run executes the binary in dir with args and returns combined output.
func run(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustRun fails the test if the command errors.
func mustRun(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, bin, dir, args...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestDogfoodFlow(t *testing.T) {
	bin := buildBinary(t)
	repo := t.TempDir()

	// init scaffolds the repo.
	out := mustRun(t, bin, repo, "init")
	for _, want := range []string{".satelle/satelle.toml", ".satelle/satelle.db", "+ .gitignore"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
	for _, rel := range []string{".satelle/satelle.toml", ".satelle/satelle.db", ".satelle/workflows/.gitkeep"} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not create %s", rel)
		}
	}

	// init is idempotent.
	out = mustRun(t, bin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something:\n%s", out)
	}

	// Create a story and a task.
	out = mustRun(t, bin, repo, "story", "create", "--title", "Dogfood satelle", "--priority", "high", "--tags", "mvp,core")
	if !strings.Contains(out, `"sty_`) || !strings.Contains(out, "Dogfood satelle") {
		t.Fatalf("story create output:\n%s", out)
	}
	storyID := extractID(out, "sty_")
	mustRun(t, bin, repo, "task", "create", "--title", "write release notes")

	// Move the story along the baseline workflow.
	out = mustRun(t, bin, repo, "story", "set", storyID, "--status", "in_progress")
	if !strings.Contains(out, `"status": "in_progress"`) {
		t.Errorf("story set status:\n%s", out)
	}

	// Lifecycle events landed in the ledger.
	out = mustRun(t, bin, repo, "ledger", "list", "--story", storyID)
	if !strings.Contains(out, "story_created") || !strings.Contains(out, "story_updated") {
		t.Errorf("ledger missing lifecycle events:\n%s", out)
	}

	// Author markdown and index it.
	docsDir := filepath.Join(repo, ".satelle", "documents")
	if err := os.WriteFile(filepath.Join(docsDir, "onboarding.md"), []byte("# Onboarding\n\nhi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = mustRun(t, bin, repo, "index")
	if !strings.Contains(out, `"indexed": 1`) {
		t.Errorf("index output:\n%s", out)
	}
	out = mustRun(t, bin, repo, "doc", "get", "documents", "onboarding")
	if !strings.Contains(out, `"headline": "Onboarding"`) {
		t.Errorf("doc get headline:\n%s", out)
	}

	// status reflects the counts.
	out = mustRun(t, bin, repo, "status")
	for _, want := range []string{"stories", "tasks", "indexed documents   1"} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
}

func TestServeServesProjectPage(t *testing.T) {
	bin := buildBinary(t)
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	mustRun(t, bin, repo, "story", "create", "--title", "Render me")

	const port = "8791"
	cmd := exec.Command(bin, "serve", "--port", port)
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

	body := httpGet(t, base+"/")
	for _, want := range []string{"Render me", "Stories", "Tasks", "satelle"} {
		if !strings.Contains(body, want) {
			t.Errorf("project page missing %q", want)
		}
	}
	if code := httpStatus(t, base+"/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}
}

// --- helpers ---

func extractID(out, prefix string) string {
	i := strings.Index(out, prefix)
	if i < 0 {
		return ""
	}
	rest := out[i:]
	end := strings.IndexAny(rest, `"`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func waitHealthy(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String()
}

func httpStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
