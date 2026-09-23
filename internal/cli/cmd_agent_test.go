package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestAgentValidatePrintsResolvedInterfaceReason (AC5, epic:model-selection
// child 2): `satelle agent validate` prints each binding's resolved interface
// AND why — explicit / one-shot default / live use — so an operator sees the
// same seam OpenSessionAs opens through without reading the source.
func TestAgentValidatePrintsResolvedInterfaceReason(t *testing.T) {
	repo := tempRepo(t)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]
rework = { consult = "consultant", rounds = 3 }

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	agentsBody := `[executor]
role    = "agent"
command = "in-loop"

[reviewer]
role  = "reviewer"
model = "opus"

[coder]
role = "agent"
tools = "Read,Grep,Glob,Edit,Write,Bash(satelle:*)"

[consultant]
role  = "reviewer"
tools = "Read,Grep,Glob,Bash(satelle:*)"
`
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agentsBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))

	out, err := runRoot(t, "agent", "validate")
	if err != nil {
		t.Fatalf("agent validate: %v\n%s", err, out)
	}
	for _, want := range []string{
		"interface=command (one-shot default)", // [reviewer]: no interface=, dispatched only by gates
		"interface=stream (live use)",          // [coder]/[consultant]: the rework relay opens both live
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agent validate output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not live-capable") {
		t.Errorf("an unset interface= used live must not WARN not-live-capable:\n%s", out)
	}
}
