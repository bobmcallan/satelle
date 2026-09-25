package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// claudeTranscriptFS serves one Claude transcript whose assistant entry carries
// the model, the way a real Claude hook payload (which has no model field)
// resolves it.
type claudeTranscriptFS struct{ body string }

func (f claudeTranscriptFS) ReadFile(string) ([]byte, error) { return []byte(f.body), nil }
func (claudeTranscriptFS) Glob(string) ([]string, error)     { return nil, nil }
func (claudeTranscriptFS) ModTime(string) (int64, error)     { return 0, errors.New("no mtime") }

// The model-inheritance column of the agent-dispatch capability table
// (sty_52a8cb4a) is derived here from the code that decides it, over BOTH
// session tiers config.SelectModel reads:
//
//   - in-loop: resolveCaller + harnessFromEvent on the captured hook payload of
//     the adapter's own harness;
//   - orchestrator: orchestratorModelCapture recording the model a live
//     orchestrator session of the same provider announces at open (ACP init /
//     stream init), under that binding's own executable, read back with
//     verb.SessionModels.
//
// Both feed SelectModel for the adapter's default binding shape; the cell is
// available when either tier is applied. A table cell that disagrees fails.
func TestCapabilityTable_ModelInheritanceMatchesCode(t *testing.T) {
	const claudeTranscript = `{"message":{"model":"claude-opus-5-5","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash"}]}}`
	type shape struct {
		hook, iface, command string
	}
	shapes := map[string]shape{
		"claude command": {"claude_bash.json", agentcli.InterfaceCommand, agentcli.DefaultClaudeCommand},
		"claude stream":  {"claude_bash.json", agentcli.InterfaceStream, agentcli.DefaultClaudeStreamCommand},
		"grok command":   {"grok_bash.json", agentcli.InterfaceCommand, agentcli.DefaultGrokCommand},
		"grok acp":       {"grok_bash.json", agentcli.InterfaceACP, "grok agent stdio"},
		"codex command":  {"codex_shell.json", agentcli.InterfaceCommand, agentcli.DefaultCodexExecCommand},
		"codex acp":      {"codex_shell.json", agentcli.InterfaceACP, agentcli.DefaultCodexACPCommand},
	}
	// The live orchestrator binding each provider can open (a command adapter is
	// never live, but inherits from a live session with the same executable).
	orchestrators := map[string]config.AgentBinding{
		"claude": {Interface: agentcli.InterfaceStream, Command: agentcli.DefaultClaudeStreamCommand},
		"grok":   {Interface: agentcli.InterfaceACP, Command: "grok agent stdio"},
		"codex":  {Interface: agentcli.InterfaceACP, Command: agentcli.DefaultCodexACPCommand},
	}
	wireLedgerOnly(t)
	for _, row := range agentcli.CapabilityTable() {
		t.Run(row.Adapter, func(t *testing.T) {
			s, ok := shapes[row.Adapter]
			if !ok {
				t.Fatalf("no inheritance probe for adapter %q", row.Adapter)
			}
			raw, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "hooks", s.hook))
			if err != nil {
				t.Fatal(err)
			}
			// In-loop tier: what publishInLoopModel records for this harness.
			model := resolveCaller(raw, claudeTranscriptFS{claudeTranscript}).Model
			if model == "" {
				model = "unknown"
			}
			inLoop := config.SessionModel{Model: model, Executable: harnessFromEvent(raw)}

			// Orchestrator tier: a live session of this provider announcing its
			// model at open, recorded under the binding's own executable.
			orchB := orchestrators[strings.Fields(row.Adapter)[0]]
			story := "sty_cap_" + strings.ReplaceAll(row.Adapter, " ", "_")
			handler, closeUnrecorded := orchestratorModelCapture(context.Background(), story, "sess", orchB.ExecutableToken(), nil)
			handler(agentcli.Event{Kind: agentcli.EventSessionInit, Model: "orchestrator-model-1"})
			closeUnrecorded()
			orch, _, _ := verb.SessionModels(context.Background(), story)

			b := config.AgentBinding{Interface: s.iface, Command: s.command}
			_, source := config.SelectModel(config.SelectInput{
				CommandExecutable: b.ExecutableToken(),
				HasModelSlot:      config.HasModelSlot(b.CommandTemplate()),
				ModelViaSession:   b.IsACP(),
				Orchestrator:      orch,
				InLoop:            inLoop,
			})
			got := source == config.ModelSourceInheritedInLoop || source == config.ModelSourceInheritedOrchestrator
			if got != row.ModelInheritance.Available {
				t.Errorf("table available=%v, code source=%q (in-loop %+v, orchestrator %+v, exe %q)",
					row.ModelInheritance.Available, source, inLoop, orch, b.ExecutableToken())
			}
			if !got && source != config.ModelSourceCLIDefault {
				t.Errorf("unavailable inheritance must fall through to cli-default, got %q", source)
			}
		})
	}
}
