//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsRepoCLIEndToEnd drives the real binary: `satelle settings <key> [value]`
// reads and writes the committed .satelle/satelle.toml (repo scope, the default),
// preserving comments; bare `settings` lists every key; and the legacy
// `settings server` still works but warns, while `--global server` does not
// (sty_e2fba595).
func TestSettingsRepoCLIEndToEnd(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	ghome := t.TempDir()
	mustRun(t, bin, repo, "init")
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")

	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "SATELLE_HOME="+ghome)
		out, err := c.CombinedOutput()
		return string(out), err
	}

	// WRITE a repo key (short key resolves to review.gate_create; a root key too).
	if out, err := run("settings", "log_level", "warn"); err != nil {
		t.Fatalf("write log_level: %v\n%s", err, out)
	}
	if out, err := run("settings", "gate_create", "false"); err != nil {
		t.Fatalf("write gate_create: %v\n%s", err, out)
	}
	saved, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(saved), `log_level = "warn"`) || !strings.Contains(string(saved), "gate_create = false") {
		t.Fatalf("repo writes not applied:\n%s", saved)
	}
	// The seeded config's comments survive the surgical upsert.
	if !strings.Contains(string(saved), "# ") {
		t.Fatalf("comments were lost by the write:\n%s", saved)
	}

	// READ one key back.
	if out, _ := run("settings", "log_level"); strings.TrimSpace(out) != "warn" {
		t.Fatalf("read log_level = %q, want warn", out)
	}

	// LIST every repo key.
	out, err := run("settings")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	for _, want := range []string{"web_port =", "log_level = warn", "review.gate_create = false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list missing %q:\n%s", want, out)
		}
	}

	// Unknown key → non-zero exit with a helpful message.
	if out, err := run("settings", "bogus", "x"); err == nil || !strings.Contains(out, "unknown repo setting") {
		t.Fatalf("unknown key should fail: err=%v out=%s", err, out)
	}

	// A repo write must NEVER touch the global config.
	if gc, _ := os.ReadFile(filepath.Join(ghome, "config.toml")); strings.Contains(string(gc), "warn") {
		t.Fatalf("repo write leaked into the global config:\n%s", gc)
	}

	// GLOBAL scope: --global server sets the hosted server with NO deprecation notice;
	// the legacy `settings server` still works but warns.
	if out, err := run("settings", "--global", "server", "https://scoped.example"); err != nil || strings.Contains(out, "deprecated") {
		t.Fatalf("--global server: err=%v, must not warn:\n%s", err, out)
	}
	if out, err := run("settings", "server", "https://legacy.example"); err != nil || !strings.Contains(out, "deprecated") {
		t.Fatalf("legacy `settings server`: err=%v, must warn:\n%s", err, out)
	}
}
