package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

func publishTestClient(server string) *hosted.Client {
	return hosted.NewClient(server, hosted.FileStore{}, nil)
}

// localAgentsFixture is this machine's agents.local.toml — inline tools and an
// absolute command, exactly what the shared file must not carry and no ingest may
// touch.
const localAgentsFixture = `[reviewer]
command = "/opt/local/bin/claude -p --output-format json --append-system-prompt {system} --allowedTools {tools} --model {model}"
tools   = "Read,Grep"
profile = "claude-opus"
env     = { ANTHROPIC_BASE_URL = "https://api.example.test" }
`

// TestPublishAdoptAndCheckWriteHostedAgentsAsIs (sty_89b9df20 AC8): `publish
// adopt` and `publish check --update` write the published agents layer exactly as
// published and never touch agents.local.toml. A catalog entry an older push left
// carrying machine-specific content is refused with the pointer to
// agents.local.toml — the authored file is left as it was.
func TestPublishAdoptAndCheckWriteHostedAgentsAsIs(t *testing.T) {
	ts := newFakePublishServer(t)
	seedCred(t, ts.URL)
	src := agentsPushRepo(t, "[hosted]\nworkspace = \"Acme\"\n", pushableAgents)
	writeRepoFile(t, src, ".satelle/workflows/agents.local.toml", localAgentsFixture)
	authored := filepath.Join(src, ".satelle", "workflows", "agents.toml")
	local := filepath.Join(src, ".satelle", "workflows", "agents.local.toml")
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	cmd, buf := testCmd()
	if err := runPublishPush(cmd, ts.URL, "", "", "", false, []string{"workflows/agents.toml"}); err != nil {
		t.Fatalf("publish push: %v\n%s", err, buf.String())
	}

	// Adopting what was just published leaves both files as they were.
	cmdA, bufA := testCmd()
	if err := runPublishAdopt(cmdA, ts.URL, "Acme", 0, "workflows/agents.toml"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, bufA.String())
	}
	if got := read(authored); got != pushableAgents {
		t.Fatalf("adopt must write the published bytes:\n%s", got)
	}
	if got := read(local); got != localAgentsFixture {
		t.Fatalf("adopt touched agents.local.toml:\n%s", got)
	}

	// A newer clean version is written as published by `check --update`.
	changed := strings.Replace(pushableAgents, `model   = "opus"`, `model   = "sonnet"`, 1)
	writeRepoFile(t, src, ".satelle/workflows/agents.toml", changed)
	cmdP, bufP := testCmd()
	if err := runPublishPush(cmdP, ts.URL, "", "", "", false, []string{"workflows/agents.toml"}); err != nil {
		t.Fatalf("publish push v2: %v\n%s", err, bufP.String())
	}
	writeRepoFile(t, src, ".satelle/workflows/agents.toml", pushableAgents)
	cmdC, bufC := testCmd()
	if err := runPublishCheck(cmdC, ts.URL, "", true); err != nil {
		t.Fatalf("check --update: %v\n%s", err, bufC.String())
	}
	if got := read(authored); got != changed {
		t.Fatalf("check --update must write the published bytes:\n%s\n--- output\n%s", got, bufC.String())
	}
	if got := read(local); got != localAgentsFixture {
		t.Fatalf("check --update touched agents.local.toml:\n%s", got)
	}

	// A legacy catalog entry that carries machine-specific content is refused.
	client := publishTestClient(ts.URL)
	if _, err := client.PublishFile(context.Background(), "ws-team", "workflows/agents.toml", "agents", "legacy", []byte(secretAgentsToml)); err != nil {
		t.Fatalf("seed legacy entry: %v", err)
	}
	writeRepoFile(t, src, ".satelle/workflows/agents.toml", pushableAgents)
	for name, run := range map[string]func() error{
		"adopt": func() error {
			c, _ := testCmd()
			return runPublishAdopt(c, ts.URL, "Acme", 0, "workflows/agents.toml")
		},
		"check --update": func() error {
			c, _ := testCmd()
			return runPublishCheck(c, ts.URL, "", true)
		},
	} {
		err := run()
		if err == nil {
			t.Fatalf("%s wrote a catalog entry carrying machine-specific content", name)
		}
		for _, want := range []string{"agents.toml [reviewer] command", ".satelle/workflows/agents.local.toml"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s refusal lacks %q:\n%v", name, want, err)
			}
		}
		if got := read(authored); got != pushableAgents {
			t.Errorf("%s altered agents.toml on a refusal:\n%s", name, got)
		}
		if got := read(local); got != localAgentsFixture {
			t.Errorf("%s altered agents.local.toml on a refusal:\n%s", name, got)
		}
	}
}
