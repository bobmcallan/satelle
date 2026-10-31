package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// TestSyncConfigAgentsPushDeployRoundTripKeepsLocalValues (sty_89b9df20 AC8): the
// personal config store holds the agents layer exactly as pushed, so a deploy
// writes it as-is and never touches this machine's agents.local.toml — where its
// inline tools and command live. Round trip 1: push then deploy into the same
// repo → agents.toml byte-identical. Round trip 2: another checkout pushes a
// changed layer; deploying it here writes those bytes and the local file is left
// alone. A legacy store body carrying machine-specific content is refused.
func TestSyncConfigAgentsPushDeployRoundTripKeepsLocalValues(t *testing.T) {
	ts := newFakeConfigServer(t)
	seedCred(t, ts.URL)

	repo := agentsPushRepo(t, "[sync]\nagents = \"personal\"\n"+boundProjectToml, pushableAgents)
	writeRepoFile(t, repo, ".satelle/workflows/agents.local.toml", localAgentsFixture)
	agentsPath := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	localPath := filepath.Join(repo, ".satelle", "workflows", "agents.local.toml")
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	cmd, buf := testCmd()
	if err := runSyncConfigPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}

	// Round trip 1: push then deploy leaves both files byte-identical.
	cmd2, buf2 := testCmd()
	if err := runSyncConfigDeploy(cmd2, ts.URL, "personal", 0); err != nil {
		t.Fatalf("deploy: %v\n%s", err, buf2.String())
	}
	if got := read(agentsPath); got != pushableAgents {
		t.Fatalf("deploy must leave agents.toml byte-identical:\n%s\n--- output\n%s", got, buf2.String())
	}
	if got := read(localPath); got != localAgentsFixture {
		t.Fatalf("deploy touched agents.local.toml:\n%s", got)
	}

	// Round trip 2: another checkout pushes a changed layer (model bumped, a
	// binding added); deploying it here writes exactly those bytes.
	other := agentsPushRepo(t, "[sync]\nagents = \"personal\"\n"+boundProjectToml, pushableAgents)
	// A checkout that has never synced must pull before it can publish over the
	// hosted snapshot (sty_fe5a8ed4); then it edits and pushes.
	cmdPull, bufPull := testCmd()
	if err := runSyncConfigDeploy(cmdPull, ts.URL, "personal", 0); err != nil {
		t.Fatalf("other pull: %v\n%s", err, bufPull.String())
	}
	changed := strings.Replace(pushableAgents, `model   = "opus"`, `model   = "sonnet"`, 1) +
		"\n[summariser]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	writeRepoFile(t, other, ".satelle/workflows/agents.toml", changed)
	cmd3, buf3 := testCmd()
	if err := runSyncConfigPush(cmd3, ts.URL, "", false); err != nil {
		t.Fatalf("push 2: %v\n%s", err, buf3.String())
	}

	pointAt(t, repo)
	cmd4, buf4 := testCmd()
	if err := runSyncConfigDeploy(cmd4, ts.URL, "personal", 0); err != nil {
		t.Fatalf("deploy 2: %v\n%s", err, buf4.String())
	}
	if got := read(agentsPath); got != changed {
		t.Fatalf("deploy must write the hosted agents.toml as pushed:\n%s", got)
	}
	if got := read(localPath); got != localAgentsFixture {
		t.Fatalf("deploy touched agents.local.toml — its inline tools and command must survive:\n%s", got)
	}

	// A legacy store body (an older push left machine-specific content in it) is
	// refused, with the pointer, and nothing local is altered.
	legacy := agentsPushRepo(t, "[sync]\nagents = \"personal\"\n[hosted]\nproject = \"legacy\"\n", pushableAgents)
	client := hosted.NewClient(ts.URL, hosted.FileStore{}, nil)
	if _, err := client.PushConfigFile(context.Background(), "legacy", "workflows/agents.toml", []byte(secretAgentsToml)); err != nil {
		t.Fatalf("seed legacy store body: %v", err)
	}
	cmd5, buf5 := testCmd()
	err := runSyncConfigDeploy(cmd5, ts.URL, "personal", 0)
	if err == nil {
		t.Fatalf("a legacy store body carrying machine-specific content was deployed:\n%s", buf5.String())
	}
	for _, want := range []string{"agents.toml [reviewer] command", "agents.toml [reviewer] env.ANTHROPIC_BASE_URL", ".satelle/workflows/agents.local.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
	if got := read(filepath.Join(legacy, ".satelle", "workflows", "agents.toml")); got != pushableAgents {
		t.Errorf("a refused deploy altered agents.toml:\n%s", got)
	}
}
