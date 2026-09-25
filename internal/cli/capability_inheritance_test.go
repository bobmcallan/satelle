package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// claudeTranscriptFS serves one Claude transcript whose assistant entry carries
// the model, the way a real Claude hook payload (which has no model field)
// resolves it.
type claudeTranscriptFS struct{ body string }

func (f claudeTranscriptFS) ReadFile(string) ([]byte, error) { return []byte(f.body), nil }
func (claudeTranscriptFS) Glob(string) ([]string, error)     { return nil, nil }
func (claudeTranscriptFS) ModTime(string) (int64, error)     { return 0, errors.New("no mtime") }

// The model-inheritance column of the agent-dispatch capability table
// (sty_52a8cb4a) is derived here from the code that decides it: the in-loop
// session tier config.SelectModel reads, built by resolveCaller +
// harnessFromEvent on the captured hook payload of the adapter's own harness.
// It feeds SelectModel for the adapter's default binding shape; the cell is
// available when that tier is applied. A table cell that disagrees fails.
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

			b := config.AgentBinding{Interface: s.iface, Command: s.command}
			_, source := config.SelectModel(config.SelectInput{
				CommandExecutable: b.ExecutableToken(),
				HasModelSlot:      config.HasModelSlot(b.CommandTemplate()),
				ModelViaSession:   b.IsACP(),
				InLoop:            inLoop,
			})
			got := source == config.ModelSourceInheritedInLoop
			if got != row.ModelInheritance.Available {
				t.Errorf("table available=%v, code source=%q (in-loop %+v, exe %q)",
					row.ModelInheritance.Available, source, inLoop, b.ExecutableToken())
			}
			if !got && source != config.ModelSourceCLIDefault {
				t.Errorf("unavailable inheritance must fall through to cli-default, got %q", source)
			}
		})
	}
}
