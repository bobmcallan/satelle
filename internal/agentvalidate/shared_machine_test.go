// sharedMachineRefs (sty_89b9df20 AC9): doctor and init warn on every rule the
// pushes refuse on — the one config.CheckAgents rule set — and only warn.
package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

func TestSharedMachineRefsReportsEveryPushRule(t *testing.T) {
	rows := []struct {
		name  string
		repo  config.AgentsConfig
		id    string
		field string
	}{
		{"a settings env literal", config.AgentsConfig{Reviewer: config.AgentBinding{Settings: map[string]any{"env": map[string]any{"BASE": "https://x.test"}}}},
			health.IDAgentsSharedMachineValue, "settings.env.BASE"},
		{"b settings secret literal", config.AgentsConfig{Reviewer: config.AgentBinding{Settings: map[string]any{"apiKey": "sk-live"}}},
			health.IDAgentsSharedMachineValue, "settings.apiKey"},
		{"c settings absolute path", config.AgentsConfig{Reviewer: config.AgentBinding{Settings: map[string]any{"permissions": map[string]any{"additionalDirectories": []any{"/srv/work"}}}}},
			health.IDAgentsSharedMachineValue, "settings.permissions.additionalDirectories[0]"},
		{"d absolute command token", config.AgentsConfig{Reviewer: config.AgentBinding{Command: "/opt/bin/claude -p {system}"}},
			health.IDAgentsSharedMachineValue, "command"},
		{"e profile", config.AgentsConfig{Reviewer: config.AgentBinding{Profile: "judge"}},
			health.IDAgentsSharedProfile, "profile"},
		{"e harness", config.AgentsConfig{Reviewer: config.AgentBinding{Harness: "claude"}},
			health.IDAgentsSharedMachineValue, "harness"},
		{"f use_global_roles", config.AgentsConfig{Defaults: config.AgentsDefaults{UseGlobalRoles: true}},
			health.IDAgentsSharedGlobalRoles, "use_global_roles"},
		{"g binding env literal", config.AgentsConfig{Agents: map[string]config.AgentBinding{"coder": {Env: map[string]string{"GOBIN": "/tmp/bin"}}}},
			health.IDAgentsSharedMachineValue, "env.GOBIN"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			fs := sharedMachineRefs(r.repo)
			if len(fs) != 1 {
				t.Fatalf("want one finding, got %v", fs)
			}
			f := fs[0]
			if f.ID != r.id || f.Severity != health.SeverityWarn {
				t.Errorf("finding = %s/%v, want a %s warning", f.ID, f.Severity, r.id)
			}
			for _, want := range []string{config.AgentsConfigName, r.field, "agents.local.toml"} {
				if !strings.Contains(f.Detail, want) {
					t.Errorf("detail %q lacks %q", f.Detail, want)
				}
			}
			if !strings.Contains(f.Remediation, "agents.local.toml") {
				t.Errorf("remediation must point at agents.local.toml: %q", f.Remediation)
			}
		})
	}
}

// A clean shared layer — ${VAR} references, blanks, relative paths — is silent,
// and the warning surfaces through the full validator as a warning, not a Problem.
func TestSharedMachineRefsCleanLayerIsSilent(t *testing.T) {
	clean := config.AgentsConfig{
		Executor: inLoopExecutor(),
		Reviewer: config.AgentBinding{Role: config.RoleReviewer, Env: map[string]string{"TOKEN": "${GLM_API_KEY}", "BLANK": ""},
			Settings: map[string]any{"env": map[string]any{"A": "${A}"}, "permissions": map[string]any{"additionalDirectories": []any{"relative/dir"}}}},
	}
	if fs := sharedMachineRefs(clean); len(fs) != 0 {
		t.Fatalf("a clean layer must not warn: %v", fs)
	}
	dirty := clean
	dirty.Reviewer.Env = map[string]string{"GOBIN": "/tmp/bin"}
	r := ValidateEffectiveLayered(config.AgentLayers{Repo: dirty}, config.GlobalAgentsConfig{}, nil, nil, nil)
	if fs := sharedFindings(r, health.IDAgentsSharedMachineValue); len(fs) != 1 || fs[0].Severity != health.SeverityWarn {
		t.Fatalf("want one machine-value warning through the validator, got %v", r.Findings)
	}
	for _, p := range r.Problems {
		if strings.Contains(p, "GOBIN") {
			t.Errorf("a shared machine value is advisory, never a Problem: %v", r.Problems)
		}
	}
}
