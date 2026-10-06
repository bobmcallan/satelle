package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// interface = "cloud" in validate (sty_82cffd60): a performer binding whose
// adapter has a cloud runner is sound; every other use is a problem.

func cloudCoder(command string) config.AgentBinding {
	return config.AgentBinding{Role: config.RoleAgent, Interface: config.InterfaceCloud, Command: command, CollectDoc: "ac-evidence"}
}

func TestValidate_CloudPerformer(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Agents["coder"] = cloudCoder("claude -p {system}")
	// Rework is not declared here, so the cloud coder is a plain performer.
	r := Validate(agents, nil, routeDocs(advisorDone, `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`))
	if !r.OK() {
		t.Fatalf("a claude cloud performer must validate: %v", r.Problems)
	}
	for _, g := range r.Grants {
		if g.Name == "coder" {
			if g.Interface != config.InterfaceCloud || g.Backend != "cloud:claude" {
				t.Errorf("grant = %+v, want interface=cloud backend cloud:claude", g)
			}
			return
		}
	}
	t.Fatal("no grant reported for the cloud coder")
}

func TestValidate_CloudWithoutARunnerIsUnavailable(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Agents["coder"] = cloudCoder(agentcli.DefaultGrokCommand)
	r := Validate(agents, nil, nil)
	joined := strings.Join(r.Problems, "\n")
	if r.OK() || !strings.Contains(joined, "[coder]") || !strings.Contains(joined, "unavailable for adapter grok") {
		t.Fatalf("a grok cloud binding must fail, naming the adapter: %v", r.Problems)
	}
}

func TestValidate_CloudOnAReviewerIsAProblem(t *testing.T) {
	agents := reworkAgents(liveConsultant())
	agents.Agents["judge"] = config.AgentBinding{Role: config.RoleReviewer, Interface: config.InterfaceCloud, Command: "claude -p {system}", Tools: "Read"}
	r := Validate(agents, nil, nil)
	if r.OK() || !strings.Contains(strings.Join(r.Problems, "\n"), "role=reviewer") {
		t.Fatalf("a cloud reviewer must fail validate: %v", r.Problems)
	}
}

func TestValidate_CloudAsAdvisorOrConsultIsAProblem(t *testing.T) {
	cloud := cloudCoder("claude -p {system}")

	agents := advisorAgents()
	agents.Agents["lessons"] = cloud
	r := Validate(agents, nil, routeDocs(advisorDone, advisorStep))
	if r.OK() || !strings.Contains(strings.Join(r.Problems, "\n"), "advisor") {
		t.Errorf("a cloud advisor must fail validate: %v", r.Problems)
	}

	agents = reworkAgents(cloud)
	r = Validate(agents, nil, routeDocs(reworkDone, reworkStep("consultant")))
	if r.OK() || !strings.Contains(strings.Join(r.Problems, "\n"), "rework consult=consultant") {
		t.Errorf("a cloud rework consult must fail validate: %v", r.Problems)
	}
}

// A cloud performer is handed the whole payload in its prompt and has no satelle
// CLI or store to pull from, so — like an in-loop binding — it needs no
// context-channel grant (config.NeedsContextChannel). A local performer with the
// same empty grant is still a problem.
func TestValidate_CloudPerformerNeedsNoContextChannel(t *testing.T) {
	mk := func(b config.AgentBinding) Report {
		agents := config.AgentsConfig{
			Executor: config.AgentBinding{Command: "in-loop"},
			Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
			Agents:   map[string]config.AgentBinding{"coder": b},
		}
		return Validate(agents, nil, channelWF("coder"))
	}

	cloud := cloudCoder("claude -p {system}")
	if cloud.Tools != "" {
		t.Fatal("fixture must grant no tools")
	}
	r := mk(cloud)
	if got := findingWith(r.Problems, "no context channel"); got != "" {
		t.Fatalf("a cloud performer with no tools must not need a context channel: %s", got)
	}
	if !r.OK() {
		t.Fatalf("cloud performer with empty tools must validate clean: %v", r.Problems)
	}

	local := mk(config.AgentBinding{Role: config.RoleAgent, Command: agentcli.DefaultClaudeCommand})
	if findingWith(local.Problems, "no context channel") == "" {
		t.Fatalf("a local performer with no tools must still be refused: %v", local.Problems)
	}
}
