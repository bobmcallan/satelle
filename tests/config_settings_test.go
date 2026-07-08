//go:build integration

// Black-box coverage for sty_964be0ea: the {settings} placeholder — a binding's
// `settings` table, ${VAR}-resolved from the [vars] KV, JSON-marshalled and passed
// INLINE as --settings <json> to the dispatched agent — driven through the REAL
// binary and its CLI wiring (config.Load's satelle.local.toml overlay →
// requireAgents' ResolveAgentEnvs → agentcli buildArgs), not just the package-level
// unit tests. Sibling to config_vars_env_test.go (which does this for per-binding env).
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnknownSettingsVarRefusesEndToEnd: a binding whose settings table references an
// undefined ${VAR} refuses a real store-backed command, naming the binding and the
// missing var (AC2) — the same fail-fast the env block gets, now for settings.
func TestUnknownSettingsVarRefusesEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Append a named binding whose settings.env references a var defined nowhere.
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n[planner]\nharness = \"claude -p {system} --settings {settings}\"\ntools = \"Read,Bash(satelle:*)\"\nsettings = { env = { ANTHROPIC_AUTH_TOKEN = \"${GLM_API_KEY}\" } }\n")
	out, err := run(t, testBin, repo, "status")
	if err == nil {
		t.Fatalf("status must refuse when a binding settings references an unknown var:\n%s", out)
	}
	for _, want := range []string{"planner", "GLM_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should name %q:\n%s", want, out)
		}
	}
}

// TestSettingsMaterializedEndToEnd drives a REAL dispatch to observe the resolved
// settings JSON reaching the subprocess as the --settings argv token, with ${VAR}
// expanded from the [vars] overlay. The stub harness echoes its argv; the dispatched
// run-output doc must carry the resolved JSON (TOKEN expanded to LOCAL) as one
// --settings arg — proving {settings} is marshalled, ${VAR}-resolved, and passed
// inline through the shipping binary (AC2).
func TestSettingsMaterializedEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A stub harness that reveals the argv it was dispatched with (one line per arg).
	script := filepath.Join(repo, "argrunner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nfor a in \"$@\"; do echo \"ARG=$a\"; done\necho \"VERIFICATION satisfied\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Named run binding: settings.env.TOKEN resolves from ${TOKEN} in [vars]; the
	// harness template carries the {settings} placeholder as its own token.
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n[runner]\nharness = \""+script+" {system} --settings {settings}\"\ntools = \"Read,Bash(satelle:*)\"\nsettings = { env = { TOKEN = \"${TOKEN}\" } }\n")
	// Gitignored overlay supplies the winning value the ${VAR} must resolve to.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[vars]\nTOKEN = \"LOCAL\"\n")

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-task-workflow.md"), dispatchTaskWorkflow)
	writeAuthoredTask(t, repo, "tsk_set01")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_set01", "--title", "settings run")
	exe := extractID(out, "exe_")
	if exe == "" {
		t.Fatalf("no execution id in:\n%s", out)
	}
	// Entering in_progress dispatches the runner with the materialised --settings arg.
	mustRun(t, testBin, repo, "execution", "set", exe, "--status", "in_progress")

	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "tasks", "tsk_set01", "output-"+exe+".md"))
	if err != nil {
		t.Fatalf("dispatched run-output doc not written: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "ARG=--settings") {
		t.Errorf("--settings flag not passed to the dispatched agent:\n%s", s)
	}
	// The resolved settings JSON must arrive with ${TOKEN} expanded to the overlay value.
	if !strings.Contains(s, `"TOKEN":"LOCAL"`) {
		t.Errorf("settings ${VAR} not resolved end-to-end (want \"TOKEN\":\"LOCAL\"):\n%s", s)
	}
	if strings.Contains(s, "${TOKEN}") {
		t.Errorf("settings placeholder left an unresolved ${VAR}:\n%s", s)
	}
}

// TestNoSettingsDropsFlagEndToEnd: a binding that authors NO settings table but whose
// harness template still carries the {settings} placeholder emits no --settings arg
// at all (the drop-preceding-flag branch, mirroring an unset {model}) — AC2's
// "a binding with no settings drops the --settings flag+placeholder".
func TestNoSettingsDropsFlagEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	script := filepath.Join(repo, "argrunner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nfor a in \"$@\"; do echo \"ARG=$a\"; done\necho \"VERIFICATION satisfied\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The {settings} placeholder is present in the template but the binding sets no
	// settings — the flag and its placeholder must both drop.
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n[runner]\nharness = \""+script+" {system} --settings {settings}\"\ntools = \"Read,Bash(satelle:*)\"\n")

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-task-workflow.md"), dispatchTaskWorkflow)
	writeAuthoredTask(t, repo, "tsk_nos01")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_nos01", "--title", "no-settings run")
	exe := extractID(out, "exe_")
	if exe == "" {
		t.Fatalf("no execution id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "execution", "set", exe, "--status", "in_progress")

	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "tasks", "tsk_nos01", "output-"+exe+".md"))
	if err != nil {
		t.Fatalf("dispatched run-output doc not written: %v", err)
	}
	if strings.Contains(string(body), "--settings") {
		t.Errorf("a binding with no settings must emit no --settings arg:\n%s", string(body))
	}
}
