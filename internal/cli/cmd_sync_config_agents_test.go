package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const authoredAgentsWithSecrets = `# authored by hand — comments must survive a no-op deploy
[executor]
role    = "agent"
command = "in-loop"

[reviewer]
role    = "reviewer"
profile = "claude-opus"
command = "/opt/local/bin/claude -p --output-format json --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "opus"
env     = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }
`

// TestSyncConfigAgentsPushDeployRoundTripKeepsLocalValues (sty_01949949
// code-ac review): the personal config store holds a REDACTED agents layer, so
// a deploy must not blank the authored file. Round trip 1: push then deploy
// into the same repo → the file is byte-identical (kept). Round trip 2: a
// changed store copy deploys with the store's layout and the local env value,
// absolute command path and profile= re-applied.
func TestSyncConfigAgentsPushDeployRoundTripKeepsLocalValues(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := syncConfigRepo(t, "[sync]\nagents = \"personal\"\n"+boundProjectToml)
	_ = os.Remove(filepath.Join(repo, ".satelle", "agents.toml")) // legacy location seeded by the helper
	writeRepoFile(t, repo, ".satelle/workflows/agents.toml", authoredAgentsWithSecrets)
	pointAt(t, repo)
	agentsPath := filepath.Join(repo, ".satelle", "workflows", "agents.toml")

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}

	// Round trip 1: nothing changed upstream → the authored bytes are untouched.
	cmd2, buf2 := testCmd()
	if err := runSyncConfigDeploy(cmd2, ts.URL, "personal", 0); err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf2.String())
	}
	got, _ := os.ReadFile(agentsPath)
	if string(got) != authoredAgentsWithSecrets {
		t.Fatalf("deploy must not rewrite an authored agents.toml the store already matches:\n%s\n--- output\n%s", got, buf2.String())
	}
	if !strings.Contains(buf2.String(), "agents: workflows/agents.toml kept") {
		t.Errorf("deploy output should say the file was kept: %s", buf2.String())
	}

	// Round trip 2: another checkout pushes a changed layer (model bumped, a
	// binding added); deploying it here must keep this machine's values.
	other := syncConfigRepo(t, "[sync]\nagents = \"personal\"\n"+boundProjectToml)
	_ = os.Remove(filepath.Join(other, ".satelle", "agents.toml"))
	changed := strings.Replace(authoredAgentsWithSecrets, `model   = "opus"`, `model   = "sonnet"`, 1) +
		"\n[summariser]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	writeRepoFile(t, other, ".satelle/workflows/agents.toml", changed)
	pointAt(t, other)
	cmd3, buf3 := testCmd()
	if err := runSyncConfigPush(cmd3, ts.URL, "", false); err != nil {
		t.Fatalf("push 2: %v\n%s", err, buf3.String())
	}

	pointAt(t, repo)
	cmd4, buf4 := testCmd()
	if err := runSyncConfigDeploy(cmd4, ts.URL, "personal", 0); err != nil {
		t.Fatalf("deploy 2: %v\n%s", err, buf4.String())
	}
	merged, _ := os.ReadFile(agentsPath)
	s := string(merged)
	for _, want := range []string{`model = "sonnet"`, `ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}"`, `/opt/local/bin/claude -p`, `profile = "claude-opus"`, "[summariser]"} {
		if !strings.Contains(s, want) {
			t.Errorf("deployed agents.toml missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, `ANTHROPIC_AUTH_TOKEN = ""`) {
		t.Errorf("deploy blanked a live env value:\n%s", s)
	}
	if !strings.Contains(buf4.String(), "re-applied") {
		t.Errorf("deploy output should say local values were re-applied: %s", buf4.String())
	}
}
