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
	for _, want := range []string{"log_level = warn", "review.gate_create = false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list missing %q:\n%s", want, out)
		}
	}

	// Unknown key → non-zero exit with a helpful message.
	if out, err := run("settings", "bogus", "x"); err == nil || !strings.Contains(out, "unknown repo setting") {
		t.Fatalf("unknown key should fail: err=%v out=%s", err, out)
	}

	// The seat concurrency mode is on the surface an agent enumerates, and its
	// section is one `satelle init` does not seed — so the write must CREATE
	// [engagement], not silently drop the edit (sty_050f3a19 AC1/AC2).
	if out, _ := run("settings", "engagement.parallel"); strings.TrimSpace(out) != "none" {
		t.Fatalf("unset engagement.parallel must resolve to the default: %q", out)
	}
	if !strings.Contains(mustList(t, run), "engagement.parallel = none") {
		t.Fatalf("list must carry engagement.parallel:\n%s", mustList(t, run))
	}
	if out, err := run("settings", "engagement.parallel", "epic"); err != nil {
		t.Fatalf("write engagement.parallel: %v\n%s", err, out)
	}
	saved, _ = os.ReadFile(cfgPath)
	if !strings.Contains(string(saved), "[engagement]") || !strings.Contains(string(saved), `parallel = "epic"`) {
		t.Fatalf("engagement write did not create the section:\n%s", saved)
	}
	if out, _ := run("settings", "engagement.parallel"); strings.TrimSpace(out) != "epic" {
		t.Fatalf("read back after write: %q", out)
	}
	// A value outside the closed set is refused, and the file is left alone.
	before, _ := os.ReadFile(cfgPath)
	if out, err := run("settings", "engagement.parallel", "all"); err == nil || !strings.Contains(out, "none | epic") {
		t.Fatalf("bogus mode must be refused naming the modes: err=%v out=%s", err, out)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatalf("refused write must not touch the file:\n%s", after)
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

// mustList runs bare `settings` and fails the test if it errors.
func mustList(t *testing.T, run func(args ...string) (string, error)) string {
	t.Helper()
	out, err := run("settings")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	return out
}
