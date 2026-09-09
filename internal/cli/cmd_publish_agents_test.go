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

// TestPublishPushRedactsAgentsLayer (sty_01949949 R1): `satelle publish push`
// of the agents layer goes through the same transport redaction as `sync
// bindings push` — the security property belongs to the agents kind, not to
// whichever verb carried the file — and an explicit --kind does not bypass it.
func TestPublishPushRedactsAgentsLayer(t *testing.T) {
	ts := newFakePublishServer(t)
	seedCred(t, ts.URL)
	src := syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, src, ".satelle/workflows/agents.toml", secretAgentsToml)
	pointAt(t, src)

	cmd, buf := testCmd()
	if err := runPublishPush(cmd, ts.URL, "", "", "", false, []string{"workflows/agents.toml"}); err != nil {
		t.Fatalf("publish push: %v\n%s", err, buf.String())
	}
	client := publishTestClient(ts.URL)
	content, meta, err := client.PublishedContent(context.Background(), "ws-team", "workflows/agents.toml", 0)
	if err != nil {
		t.Fatalf("fetch published: %v", err)
	}
	if meta.Kind != "agents" {
		t.Errorf("kind = %q, want agents", meta.Kind)
	}
	s := string(content)
	for _, leak := range []string{"https://literal.example", "/opt/local/bin", "live-secret"} {
		if strings.Contains(s, leak) {
			t.Errorf("publish push leaked %q:\n%s", leak, s)
		}
	}
	if !strings.Contains(s, "ANTHROPIC_AUTH_TOKEN") || !strings.Contains(s, "${GLM_API_KEY}") || !strings.Contains(s, "claude -p --output-format json") {
		t.Errorf("publish push over-redacted:\n%s", s)
	}

	// An explicit --kind for the agents path does not bypass redaction: the
	// property follows the PATH, not the caller's label.
	cmdK, bufK := testCmd()
	if err := runPublishPush(cmdK, ts.URL, "", "workflow", "", false, []string{"workflows/agents.toml"}); err != nil {
		t.Fatalf("publish push --kind workflow: %v\n%s", err, bufK.String())
	}
	contentK, _, err := client.PublishedContent(context.Background(), "ws-team", "workflows/agents.toml", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contentK), "https://literal.example") || strings.Contains(string(contentK), "/opt/local/bin") {
		t.Errorf("explicit --kind bypassed agents redaction:\n%s", contentK)
	}

	// Adopting the (redacted) catalog entry over the authored file it came from
	// leaves the authored bytes untouched: the store holds nothing the repo lacks.
	authored := filepath.Join(src, ".satelle", "workflows", "agents.toml")
	before, _ := os.ReadFile(authored)
	cmdA, bufA := testCmd()
	if err := runPublishAdopt(cmdA, ts.URL, "Acme", 0, "workflows/agents.toml"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, bufA.String())
	}
	after, _ := os.ReadFile(authored)
	if string(after) != string(before) {
		t.Fatalf("adopt must not overwrite the authored agents.toml with the redacted copy:\n%s", after)
	}
	if !strings.Contains(bufA.String(), "kept") {
		t.Errorf("adopt output should say the file was kept: %s", bufA.String())
	}
}
