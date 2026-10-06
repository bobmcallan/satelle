package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// remote_agent / local_tags on a spine performer step (sty_dde8b6a4): the cloud
// binding that performs a parallel epic child's step. It must resolve to a
// role=agent, interface=cloud binding and never sits on a container step.

const remoteDone = `["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "merged", "closed"]
`

func remoteSteps(codedExtra, readyExtra, mergedExtra string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
` + codedExtra + `
requires = ["raised"]

[ready]
status = "ready"
agent = "executor"
waits_on_children = true
schedule = "parallel"
` + readyExtra + `
requires = ["raised"]

[merged]
status = "merging"
agent = "executor"
` + mergedExtra + `
requires = ["ready"]

[closed]
status = "done"
terminal = true
requires = ["raised"]
`
}

func remoteAgents() config.AgentsConfig {
	agents := reworkAgents(liveConsultant())
	agents.Agents["coder"] = config.AgentBinding{Role: config.RoleAgent, Command: "claude -p {system}", Tools: "Bash(satelle:*)"}
	agents.Agents["coder-cloud"] = cloudCoder("claude -p {system}")
	return agents
}

func TestValidate_RemoteAgent(t *testing.T) {
	const declared = "remote_agent = \"coder-cloud\"\nlocal_tags = [\"lane:trunk\"]"
	reviewerCloud := func(a *config.AgentsConfig) {
		a.Agents["coder-cloud"] = config.AgentBinding{Role: config.RoleReviewer, Interface: config.InterfaceCloud, Command: "claude -p {system}", Tools: "Read"}
	}
	cases := []struct {
		name    string
		mutate  func(*config.AgentsConfig)
		coded   string
		ready   string
		merged  string
		wantErr string // "" = must validate clean
	}{
		{name: "cloud binding accepted and counted as used", coded: declared},
		{name: "no declaration unchanged"},
		{name: "missing binding", coded: `remote_agent = "ghost"`, wantErr: `step "in_progress" declares remote_agent=ghost with no [ghost] binding`},
		{name: "non-cloud binding", coded: `remote_agent = "coder"`, wantErr: "want role=agent interface=cloud"},
		{name: "reviewer-role cloud binding", mutate: reviewerCloud, coded: `remote_agent = "coder-cloud"`, wantErr: "want role=agent interface=cloud"},
		{name: "container step waits_on_children", ready: `remote_agent = "coder-cloud"`, wantErr: `step "ready" declares remote_agent=coder-cloud on a container step`},
		{name: "container step after_children", merged: "remote_agent = \"coder-cloud\"\nafter_children = \"coded\"", wantErr: `step "merging" declares remote_agent=coder-cloud on a container step`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agents := remoteAgents()
			if tc.mutate != nil {
				tc.mutate(&agents)
			}
			r := Validate(agents, nil, routeDocs(remoteDone, remoteSteps(tc.coded, tc.ready, tc.merged)))
			joined := strings.Join(r.Problems, "\n")
			if tc.wantErr == "" {
				if !r.OK() {
					t.Fatalf("must validate clean: %v", r.Problems)
				}
				orphaned := len(reworkFindings(t, r, "[coder-cloud]")) > 0
				if declared := tc.coded != ""; declared == orphaned {
					t.Errorf("[coder-cloud] orphaned = %v with remote_agent declared = %v: a remote_agent binding must count as used, an undeclared one is orphaned", orphaned, declared)
				}
				return
			}
			if r.OK() || !strings.Contains(joined, tc.wantErr) {
				t.Fatalf("want a problem containing %q, got %v", tc.wantErr, r.Problems)
			}
		})
	}
}
