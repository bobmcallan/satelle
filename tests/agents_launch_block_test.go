//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentsLaunch_ConfigurationKeepsACredentialFromTheAgent (sty_b8310408
// AC7): launch steps keep a credential from the agent through configuration
// alone, with no cloud account, network or operator setup. Fly.io is only the
// example the catalog below declares; the binary knows nothing about it, and
// blocking Fly.io in a real repo is that repo's own configuration.
func TestAgentsLaunch_ConfigurationKeepsACredentialFromTheAgent(t *testing.T) {
	const sentinel = "SENTINEL-FLY-TOKEN-5ca1ab1e"
	home := isolatedHome(t)
	repo := t.TempDir()
	wf := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	envLog := filepath.Join(t.TempDir(), "claude.env")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, ".satelle", "satelle.toml"), "[review]\ngate_create = false\n", 0o644)
	write(filepath.Join(wf, "agents.toml"), "[executor]\ncommand = \"in-loop\"\n", 0o644)
	write(filepath.Join(wf, "agents.local.toml"), "[executor]\ncommand = \"in-loop\"\nprofile = \"dev\"\n", 0o644)
	write(filepath.Join(home, "agents.toml"), `
[profiles.dev]
role = "agent"
command = "in-loop"

[profiles.dev.launch]
unset = ["FLY_API_TOKEN", "FLY_ACCESS_TOKEN"]
set = { FLY_CONFIG_DIR = "${SATELLE_PROFILE_DIR}/fly" }
`, 0o644)
	// The fake harness records the environment it was started with.
	write(filepath.Join(bin, "claude"), "#!/bin/sh\nenv > \""+envLog+"\"\n", 0o755)

	out, err := runEnv(t, testBin, repo, []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FLY_API_TOKEN=" + sentinel,
		"FLY_ACCESS_TOKEN=" + sentinel,
		"FLY_CONFIG_DIR=",
		"SATELLE_LAUNCHER=",
	}, "agents", "launch", "claude")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, out)
	}
	if strings.Contains(out, sentinel) {
		t.Errorf("launcher output carries the token:\n%s", out)
	}
	raw, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatalf("fake harness did not run: %v\n%s", err, out)
	}
	env := string(raw)
	if strings.Contains(env, sentinel) {
		t.Fatalf("the launched harness received the token:\n%s", env)
	}
	for _, gone := range []string{"FLY_API_TOKEN=", "FLY_ACCESS_TOKEN="} {
		if strings.Contains(env, "\n"+gone) || strings.HasPrefix(env, gone) {
			t.Errorf("the launched harness still has %s", gone)
		}
	}
	want := filepath.Join(home, "profiles", "dev", "fly")
	if !strings.Contains(env, "\nFLY_CONFIG_DIR="+want+"\n") {
		t.Fatalf("FLY_CONFIG_DIR is not the profile's own directory %s:\n%s", want, env)
	}
	// The directory the tool is pointed at holds no credential: it is empty or absent.
	if entries, err := os.ReadDir(want); err == nil && len(entries) > 0 {
		t.Errorf("%s is not empty: %v", want, entries)
	}
}
