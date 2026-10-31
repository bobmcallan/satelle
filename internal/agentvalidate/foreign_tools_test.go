package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// A profile in the machine-wide catalog moved a seat to the Claude CLI; the
// repo's agents.local.toml still restates its tools in pi's names. The layered
// validate (doctor, `satelle agent validate`) names the repo, the file, the seat
// and the names (sty_259572f6 AC2).
func TestValidateEffectiveLayered_ForeignLayerToolsNamed(t *testing.T) {
	global, err := config.ParseGlobalAgents(`
[profiles.pi-planner]
role    = "agent"
command = "` + agentcli.DefaultClaudeCommand + `"
model   = "opus"
`)
	if err != nil {
		t.Fatal(err)
	}
	repo := config.AgentsConfig{Executor: inLoopExecutor(), Reviewer: config.AgentBinding{Role: config.RoleReviewer, Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir"}}
	run := func(tools string) []health.Finding {
		local := config.AgentsConfig{Agents: map[string]config.AgentBinding{"planner": {Profile: "pi-planner", Tools: tools}}}
		r := ValidateEffectiveLayered(config.AgentLayers{Repo: repo, Local: local, RepoRoot: "/src/id4me"}, global, nil, nil, nil)
		var out []health.Finding
		for _, f := range r.Findings {
			if f.Title == "Tools named for another adapter" {
				out = append(out, f)
			}
		}
		return out
	}

	fs := run("read,grep,find,ls")
	if len(fs) != 1 {
		t.Fatalf("want one finding for [planner], got %v", fs)
	}
	f := fs[0]
	if f.Severity != health.SeverityWarn || f.ID != health.IDAgentsBinding {
		t.Errorf("want a %s warning, got %s %s", health.IDAgentsBinding, f.ID, f.Severity)
	}
	for _, want := range []string{"/src/id4me", ".satelle/workflows/agents.local.toml", "[planner]", "pi", "claude", "read,grep,find,ls"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("finding must name %q: %s", want, f.Detail)
		}
	}

	if fs := run("Read,Grep,Glob"); len(fs) != 0 {
		t.Errorf("a grant in the resolved adapter's names must raise nothing, got %v", fs)
	}
}
