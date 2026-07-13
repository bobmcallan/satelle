//go:build integration

// Black-box coverage for sty_b53730e2: `satelle story retrospect <id>` dispatches
// the [retrospective] named agent over a finished story and records the dispatch's
// cost on an agent_invocation entry (so `satelle story cost` rolls it up). Driven
// through the REAL binary with a stub harness so no live model is called.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retroScript is a stub retrospective agent: it consumes the payload and files one
// proposal story via the satelle CLI (proving the agent can create backlog items),
// then reports. It echoes a distinctive marker captured as the dispatch output.
const retroScript = `#!/bin/sh
cat > /dev/null
satelle story create --title "proposal from retro" --category refactor --tags "retrospective:test" --body "b" --acceptance "1. done" >/dev/null 2>&1
echo "RETRO-RAN"
`

func TestStoryRetrospectDispatchesAndRecordsCost(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	script := filepath.Join(repo, "retro.sh")
	if err := os.WriteFile(script, []byte(retroScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// A [retrospective] binding pointed at the stub, with the satelle CLI grant.
	f, err := os.OpenFile(filepath.Join(repo, ".satelle", "agents.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[retrospective]\ncommand = \"" + script + " {system}\"\ntools = \"Read,Bash(satelle:*)\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// A finished story to retrospect on.
	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "shipped thing", "--body", "b", "--acceptance", "1. x")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	mustRun(t, testBin, repo, "story", "retrospect", id)

	// The stub filed a proposal story tagged retrospective:test.
	list := mustRun(t, testBin, repo, "story", "list")
	if !strings.Contains(list, "proposal from retro") {
		t.Errorf("retrospective did not file a proposal story:\n%s", list)
	}
	// The dispatch is recorded on the ledger so `story cost` can surface it.
	led := mustRun(t, testBin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(led, "retrospective on "+id) {
		t.Errorf("retrospective dispatch not recorded on the ledger:\n%s", led)
	}
}
