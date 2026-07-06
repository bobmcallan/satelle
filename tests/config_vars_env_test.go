//go:build integration

// Black-box coverage for sty_001558ce: per-binding env + ${VAR} substitution from
// the [vars] KV, driven through the REAL binary and its CLI wiring (config.Load's
// satelle.local.toml overlay → requireAgents' ResolveAgentEnvs → the dispatch env
// injection), not just the package-level unit tests.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnknownEnvVarRefusesEndToEnd: a binding whose env references an undefined
// ${VAR} refuses a real store-backed command, naming the binding and the missing
// var (AC1) — the same fail-fast a malformed agents.toml gets, but for env vars.
func TestUnknownEnvVarRefusesEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Append a named binding that references a var defined nowhere.
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n[planner]\nharness = \"claude -p {system}\"\ntools = \"Read,Bash(satelle:*)\"\nenv = { ANTHROPIC_AUTH_TOKEN = \"${GLM_API_KEY}\" }\n")
	out, err := run(t, testBin, repo, "status")
	if err == nil {
		t.Fatalf("status must refuse when a binding env references an unknown var:\n%s", out)
	}
	for _, want := range []string{"planner", "GLM_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should name %q:\n%s", want, out)
		}
	}
}

// TestEnvOverlayWinsEndToEnd drives a REAL dispatch to observe the resolved value:
// the committed satelle.toml [vars] sets TOKEN=COMMITTED, the gitignored
// satelle.local.toml overrides it to TOKEN=LOCAL, and a named-agent run step's env
// references ${TOKEN}. The dispatched stub echoes its TOKEN environment, captured
// as the execution's OKF run-output doc — proving the overlay wins AND that the env
// is injected into the child, both through the shipping binary (AC1/AC4).
func TestEnvOverlayWinsEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// A stub harness that reveals the resolved TOKEN env it was dispatched with.
	script := filepath.Join(repo, "envrunner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\necho \"OBSERVED-TOKEN=$TOKEN\"\necho \"VERIFICATION satisfied\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Named run binding: env TOKEN resolves from ${TOKEN} in the [vars] KV. The
	// pinned model is recorded on the dispatch (audit signal for model mixing).
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n[runner]\nharness = \""+script+" {system}\"\ntools = \"Read,Bash(satelle:*)\"\nmodel = \"glm-4.6\"\nenv = { TOKEN = \"${TOKEN}\" }\n")
	// Committed vars: a decoy the overlay must beat.
	appendToFile(t, filepath.Join(repo, ".satelle", "satelle.toml"), "\n[vars]\nTOKEN = \"COMMITTED\"\n")
	// Gitignored overlay: the winning value.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[vars]\nTOKEN = \"LOCAL\"\n")

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "satelle-task-workflow.md"), dispatchTaskWorkflow)
	writeAuthoredTask(t, repo, "tsk_env01")
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "execution", "create", "--parent", "tsk_env01", "--title", "env run")
	exe := extractID(out, "exe_")
	if exe == "" {
		t.Fatalf("no execution id in:\n%s", out)
	}
	// Entering in_progress dispatches the runner with the resolved env.
	mustRun(t, testBin, repo, "execution", "set", exe, "--status", "in_progress")

	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "tasks", "tsk_env01", "output-"+exe+".md"))
	if err != nil {
		t.Fatalf("dispatched run-output doc not written: %v", err)
	}
	if !strings.Contains(string(body), "OBSERVED-TOKEN=LOCAL") {
		t.Errorf("overlay did not win end-to-end (want OBSERVED-TOKEN=LOCAL):\n%s", body)
	}
	if strings.Contains(string(body), "OBSERVED-TOKEN=COMMITTED") {
		t.Errorf("committed var wrongly won over the local overlay:\n%s", body)
	}
	// The ledger records WHICH model ran the step (per-step mixing audit trail),
	// without ever recording the env (endpoint/token).
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", exe)
	if !strings.Contains(led, "on model glm-4.6") {
		t.Errorf("dispatch ledger should record the resolved model:\n%s", led)
	}
	if strings.Contains(led, "LOCAL") || strings.Contains(led, "ANTHROPIC_") {
		t.Errorf("env (endpoint/token) must never appear in the ledger:\n%s", led)
	}
}

// TestDefaultConfigNeedsNoGLMKey pins AC2: the committed default (an Anthropic
// planner with the GLM binding present only as an inert COMMENT) boots with NO
// satelle.local.toml and no GLM key — a clone without a key still runs; GLM is
// strictly opt-in (sty_5d48317b).
func TestDefaultConfigNeedsNoGLMKey(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// A commented GLM opt-in block, exactly as the committed agents.toml carries it —
	// inert unless the operator activates it. No [vars], no key anywhere.
	appendToFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"\n# OPT-IN GLM:\n# [planner]\n# model = \"glm-4.6\"\n# env = { ANTHROPIC_AUTH_TOKEN = \"${GLM_API_KEY}\" }\n")
	out := mustRun(t, testBin, repo, "status")
	if !strings.Contains(out, "repo root") {
		t.Errorf("the default config must boot with no GLM key present:\n%s", out)
	}
}

// appendToFile appends s to an existing file (created by init), failing the test
// on error.
func appendToFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
