//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hostedStatusEnv isolates the machine home (SATELLE_HOME) and gives the run a
// fresh credential-store dir, so no sign-in exists.
func hostedStatusEnv(t *testing.T, ghome string) []string {
	t.Helper()
	return []string{"SATELLE_HOME=" + ghome, "XDG_CONFIG_HOME=" + t.TempDir()}
}

func writeHostedServer(t *testing.T, ghome, server string) {
	t.Helper()
	body := "[hosted]\nserver = \"" + server + "\"\n"
	if err := os.WriteFile(filepath.Join(ghome, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStatusShowsBoundProject (sty_1c4ad7e2 AC1): a bound checkout shows the
// project slug and the resolved hosted server.
func TestStatusShowsBoundProject(t *testing.T) {
	repo, ghome := t.TempDir(), t.TempDir()
	env := hostedStatusEnv(t, ghome)
	mustRunEnv(t, testBin, repo, env, "init")
	mustRunEnv(t, testBin, repo, env, "project", "bind", "alpha")

	out := mustRunEnv(t, testBin, repo, env, "status")
	if !regexp.MustCompile(`(?m)^hosted project\s+alpha$`).MatchString(out) {
		t.Errorf("no bound hosted project row:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^hosted server\s+https://satelle\.dev$`).MatchString(out) {
		t.Errorf("no default hosted server row:\n%s", out)
	}

	writeHostedServer(t, ghome, "https://example.test")
	out = mustRunEnv(t, testBin, repo, env, "status")
	if !regexp.MustCompile(`(?m)^hosted server\s+https://example\.test$`).MatchString(out) {
		t.Errorf("configured server not shown:\n%s", out)
	}
}

// TestStatusShowsNotBound (AC2): no explicit binding reads as not bound, with
// the derived slug and the bind remedy.
func TestStatusShowsNotBound(t *testing.T) {
	repo, ghome := t.TempDir(), t.TempDir()
	env := hostedStatusEnv(t, ghome)
	mustRunEnv(t, testBin, repo, env, "init")

	out := mustRunEnv(t, testBin, repo, env, "status")
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "hosted project") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no hosted project row:\n%s", out)
	}
	derived := strings.ToLower(filepath.Base(repo))
	for _, want := range []string{"not bound", "satelle project bind", derived} {
		if !strings.Contains(strings.ToLower(row), want) {
			t.Errorf("row %q missing %q", row, want)
		}
	}
}

// TestStatusHostedRowsOfflineSignedOut (AC3): an unreachable server and no
// credentials still render the rows and exit 0 — status makes no network call.
func TestStatusHostedRowsOfflineSignedOut(t *testing.T) {
	repo, ghome := t.TempDir(), t.TempDir()
	env := hostedStatusEnv(t, ghome)
	mustRunEnv(t, testBin, repo, env, "init")
	writeHostedServer(t, ghome, "http://127.0.0.1:1")

	out := mustRunEnv(t, testBin, repo, env, "status") // fails the test on non-zero exit
	if !regexp.MustCompile(`(?m)^hosted server\s+http://127\.0\.0\.1:1$`).MatchString(out) {
		t.Errorf("unreachable server not shown:\n%s", out)
	}
	if !strings.Contains(out, "hosted project") {
		t.Errorf("no hosted project row:\n%s", out)
	}
}
