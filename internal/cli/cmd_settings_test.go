package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// repoConfig seeds a temp repo satelle.toml and points config.Load at it via
// SATELLE_CONFIG, returning its path.
func repoConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".satelle", "satelle.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLE_CONFIG", cfgPath)
	return cfgPath
}

// TestSettingsServerSetPrintClear proves `satelle settings server` manages the
// global hosted server WITHOUT any login, and never writes a token (sty_432bdeb7).
func TestSettingsServerSetPrintClear(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())

	// Unset → a helpful "not configured" line.
	cmd, buf := testCmd()
	if err := runSettingsServer(cmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no global hosted server") {
		t.Fatalf("unset print unexpected: %q", buf.String())
	}

	// Set (no auth) → lands in the global config, normalized.
	cmd, _ = testCmd()
	if err := runSettingsServer(cmd, []string{"https://h/"}, false); err != nil {
		t.Fatal(err)
	}
	gc, _ := config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "https://h" {
		t.Fatalf("server not set: %+v", gc.Hosted)
	}

	// Print → shows the set value.
	cmd, buf = testCmd()
	if err := runSettingsServer(cmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "https://h") {
		t.Fatalf("print set value unexpected: %q", buf.String())
	}

	// No token is ever written to the global config.
	body, _ := os.ReadFile(config.GlobalConfigPath())
	if strings.Contains(string(body), "token") {
		t.Fatalf("global config must not contain a token:\n%s", body)
	}

	// Clear → removed.
	cmd, _ = testCmd()
	if err := runSettingsServer(cmd, nil, true); err != nil {
		t.Fatal(err)
	}
	gc, _ = config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "" {
		t.Fatalf("server not cleared: %+v", gc.Hosted)
	}
}

// TestSettingsRepoReadWriteRoundTrip proves the repo scope reads and writes committed
// satelle.toml keys through the surgical upsert — comments and unmodeled keys survive.
func TestSettingsRepoReadWriteRoundTrip(t *testing.T) {
	cfgPath := repoConfig(t, "# keep me\nlog_level = \"info\"\nfuture_key = \"x\"\n\n[review]\ngate_create = true\n")

	for _, kv := range [][2]string{{"log_level", "warn"}, {"gate_create", "false"}, {"edit_exempt_paths", ".claude/,vendor/"}} {
		cmd, _ := testCmd()
		if err := runSettings(cmd, []string{kv[0], kv[1]}, false); err != nil {
			t.Fatalf("write %s=%s: %v", kv[0], kv[1], err)
		}
	}

	// Read one key back through the CLI.
	cmd, buf := testCmd()
	if err := runSettings(cmd, []string{"log_level"}, false); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "warn" {
		t.Fatalf("log_level read = %q", buf.String())
	}

	// Comment + unmodeled key survived the writes.
	saved, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(saved), "# keep me") || !strings.Contains(string(saved), `future_key = "x"`) {
		t.Fatalf("surgical upsert lost content:\n%s", saved)
	}
	// Values applied.
	cfg, _, _ := config.Load(cfgPath)
	if cfg.LogLevel != "warn" || cfg.Review.GateCreate {
		t.Fatalf("values not applied: %+v", cfg)
	}
	if len(cfg.Gate.EditExemptPaths) != 2 {
		t.Fatalf("list not applied: %+v", cfg.Gate.EditExemptPaths)
	}
}

// TestSettingsRepoList proves bare `satelle settings` lists every repo key + value.
func TestSettingsRepoList(t *testing.T) {
	repoConfig(t, "log_level = \"debug\"\n")
	cmd, buf := testCmd()
	if err := runSettings(cmd, nil, false); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	for _, id := range config.SettingIDs() {
		if !strings.Contains(body, id+" =") {
			t.Errorf("list missing key %q", id)
		}
	}
	if !strings.Contains(body, "log_level = debug") {
		t.Errorf("list missing resolved value:\n%s", body)
	}
	if strings.Contains(body, "web_port") {
		t.Errorf("repo list must not include web_port:\n%s", body)
	}
}

// TestSettingsRepoErrors covers unknown keys, the substrate_roots read-only exception,
// value validation, and the global-scope one-key rule.
func TestSettingsRepoErrors(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repoConfig(t, "log_level = \"info\"\n")
	cases := []struct {
		name   string
		args   []string
		global bool
		want   string
	}{
		{"unknown key", []string{"nope", "x"}, false, "unknown repo setting"},
		{"substrate_roots read-only", []string{"substrate_roots", "x"}, false, "read-only"},
		{"stray web_port", []string{"web_port", "9000"}, false, "satelle migrate --yes"},
		{"bad bool", []string{"gate_create", "yes"}, false, "true or false"},
		{"bad enum", []string{"log_level", "loud"}, false, "must be one of"},
		{"global unknown key", []string{"nope", "9"}, true, "unknown global setting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := testCmd()
			err := runSettings(cmd, tc.args, tc.global)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// TestSettingsRepoNoConfig proves a repo read outside any satelle repo gives a clear
// not-found error.
func TestSettingsRepoNoConfig(t *testing.T) {
	t.Setenv("SATELLE_CONFIG", "")
	t.Chdir(t.TempDir())
	cmd, _ := testCmd()
	err := runSettings(cmd, []string{"log_level"}, false)
	if err == nil || !strings.Contains(err.Error(), "no .satelle/satelle.toml found") {
		t.Fatalf("got %v, want not-found error", err)
	}
}

func TestSettingsGlobalListenKeys(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	cmd, _ := testCmd()
	if err := runSettings(cmd, []string{"port", "9099"}, true); err != nil {
		t.Fatal(err)
	}
	cmd, buf := testCmd()
	if err := runSettings(cmd, []string{"port"}, true); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "9099" {
		t.Fatalf("port read = %q", buf.String())
	}
	cmd, _ = testCmd()
	if err := runSettings(cmd, []string{"addr", "127.0.0.1"}, true); err != nil {
		t.Fatal(err)
	}
	cmd, _ = testCmd()
	if err := runSettings(cmd, []string{"endpoint", "http://127.0.0.1:9099"}, true); err != nil {
		t.Fatal(err)
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Service.Port != 9099 || gc.Service.Addr != "127.0.0.1" || gc.Service.Endpoint != "http://127.0.0.1:9099" {
		t.Fatalf("global listen keys = %+v", gc.Service)
	}
	cmd, buf = testCmd()
	if err := runSettings(cmd, nil, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"port = 9099", "addr = 127.0.0.1", "endpoint = http://127.0.0.1:9099"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("list missing %q:\n%s", want, buf.String())
		}
	}
}

// TestSettingsServerDeprecationNotice proves the legacy `settings server` path still
// works but warns, while the scoped `--global server` path does not — for both flag
// orders.
func TestSettingsServerDeprecationNotice(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	run := func(args ...string) (string, string) {
		cmd := newSettingsCommand()
		var out, errb bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute %v: %v (stderr=%s)", args, err, errb.String())
		}
		return out.String(), errb.String()
	}

	if _, errb := run("server", "https://h"); !strings.Contains(errb, "deprecated") {
		t.Errorf("legacy `settings server` should warn, stderr=%q", errb)
	}
	if _, errb := run("--global", "server", "https://h2"); strings.Contains(errb, "deprecated") {
		t.Errorf("`--global server` must not warn, stderr=%q", errb)
	}
	if _, errb := run("server", "--global", "https://h3"); strings.Contains(errb, "deprecated") {
		t.Errorf("`server --global` (flag after subcommand) must not warn, stderr=%q", errb)
	}
	// The scoped writes landed in the global config.
	gc, _ := config.LoadGlobal()
	if gc.Hosted.ResolveServer() != "https://h3" {
		t.Fatalf("global server = %q, want https://h3", gc.Hosted.ResolveServer())
	}
}
