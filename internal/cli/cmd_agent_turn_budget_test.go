package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// `satelle agent validate` reports every binding's effective turn budget and
// where it comes from, whatever the adapter: a grok reviewer and a grok agent
// from the command's own --max-turns, and a claude binding with no turn flag by
// an adapter-named none reason.
func TestAgentValidateReportsEffectiveTurnBudget(t *testing.T) {
	repo := tempRepo(t)
	agentsBody := `[executor]
role    = "agent"
command = "in-loop"

[reviewer]
role    = "reviewer"
command = "` + agentcli.DefaultGrokReviewerCommand + `"

[coder]
role    = "agent"
command = "` + agentcli.DefaultGrokCommand + `"

[planner]
role    = "agent"
command = "` + agentcli.DefaultClaudeCommand + `"
`
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agentsBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))

	out, err := runRoot(t, "agent", "validate")
	if err != nil {
		t.Fatalf("agent validate: %v\n%s", err, out)
	}
	for _, want := range []string{
		"turn budget: 32 (command --max-turns)",
		"turn budget: 16 (command --max-turns)",
		"turn budget: none (claude command: the command template carries no {max_turns} placeholder",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agent validate output missing %q:\n%s", want, out)
		}
	}
}
