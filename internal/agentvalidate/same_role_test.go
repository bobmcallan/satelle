// sty_29ce243b: the executor and orchestrator seats both resolving to in-loop is
// ONE agent holding both roles. It is reported as an advisory Warn — never a
// Problem — and only when BOTH seats' resolved commands are in-loop.
package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

func sameRoleFindings(r Report) health.Findings {
	var out health.Findings
	for _, f := range r.Findings {
		if f.ID == health.IDAgentsSameRole {
			out = append(out, f)
		}
	}
	return out
}

func TestSameRoleFinding(t *testing.T) {
	inLoop := config.AgentBinding{Role: config.RoleAgent, Command: "in-loop"}
	isolated := config.AgentBinding{Role: config.RoleAgent, Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)"}
	unset := config.AgentBinding{Role: config.RoleAgent}

	cases := []struct {
		name string
		exec config.AgentBinding
		orch *config.AgentBinding
		want bool
	}{
		{"both in-loop", inLoop, &inLoop, true},
		{"executor unset resolves in-loop, orchestrator in-loop", unset, &inLoop, true},
		{"in-loop token is case-insensitive", config.AgentBinding{Command: "IN-LOOP"}, &inLoop, true},
		{"executor in-loop, orchestrator isolated", inLoop, &isolated, false},
		{"orchestrator in-loop, executor isolated", isolated, &inLoop, false},
		{"executor in-loop, orchestrator unset resolves isolated", inLoop, &unset, false},
		{"executor unset, orchestrator unset", unset, &unset, false},
		{"executor in-loop, orchestrator absent", inLoop, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agents := config.AgentsConfig{Executor: c.exec, Agents: map[string]config.AgentBinding{}}
			if c.orch != nil {
				agents.Agents["orchestrator"] = *c.orch
			}
			f, got := sameRoleFinding(agents)
			if got != c.want {
				t.Fatalf("sameRoleFinding ok = %v, want %v", got, c.want)
			}
			if got && f.Severity != health.SeverityWarn {
				t.Errorf("severity = %v, want warn", f.Severity)
			}
		})
	}
}

func TestEffectiveValidateReportsSameRoleAsWarning(t *testing.T) {
	inLoop := config.AgentBinding{Role: config.RoleAgent, Command: "in-loop"}
	repo := config.AgentsConfig{
		Executor: inLoop,
		Reviewer: config.AgentBinding{Role: config.RoleReviewer, Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob"},
		Agents:   map[string]config.AgentBinding{"orchestrator": inLoop},
	}
	r := ValidateEffectiveLayered(repo, config.AgentsConfig{}, config.GlobalAgentsConfig{}, nil, nil, nil)

	fs := sameRoleFindings(r)
	if len(fs) != 1 {
		t.Fatalf("want exactly one %s finding, got %v", health.IDAgentsSameRole, r.Findings)
	}
	if fs[0].Severity != health.SeverityWarn {
		t.Errorf("severity = %v, want warn", fs[0].Severity)
	}
	for _, seat := range []string{"[executor]", "[orchestrator]"} {
		if !strings.Contains(fs[0].Detail, seat) {
			t.Errorf("detail must name %s: %q", seat, fs[0].Detail)
		}
	}
	for _, p := range r.Problems {
		if strings.Contains(p, "same role") {
			t.Errorf("the advisory must never be a Problem: %q", p)
		}
	}
	found := false
	for _, w := range r.Warnings {
		found = found || w == fs[0].Detail
	}
	if !found {
		t.Errorf("detail missing from Warnings: %v", r.Warnings)
	}
}

// The shipped baseline declares [orchestrator] with no command (isolated
// default) beside the in-loop executor: one seat in-loop, so no warning.
func TestEffectiveValidateBaselineShapeDoesNotWarn(t *testing.T) {
	repo := config.AgentsConfig{
		Executor: config.AgentBinding{Role: config.RoleAgent, Command: "in-loop"},
		Reviewer: config.AgentBinding{Role: config.RoleReviewer, Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob"},
	}
	r := ValidateEffectiveLayered(repo, config.AgentsConfig{}, config.GlobalAgentsConfig{}, nil, nil, nil)
	if fs := sameRoleFindings(r); len(fs) != 0 {
		t.Fatalf("baseline shape must not warn, got %v", fs)
	}
}
