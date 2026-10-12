// The machine-local agents layer (agents.local.toml, sty_5c56143b) through the
// validator: a shared profile=/use_global_roles is warned about, a local one is
// not, and a seat declared only locally is a resolved, allocated binding.
package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

const localCatalog = `
[profiles.judge]
role    = "reviewer"
command = "` + agentcli.DefaultClaudeCommand + `"
tools   = "Read,Grep,Glob"
model   = "opus"

[roles]
reviewer = "judge"
`

func localCatalogConfig(t *testing.T) config.GlobalAgentsConfig {
	t.Helper()
	g, err := config.ParseGlobalAgents(localCatalog)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func sharedFindings(r Report, id string) []health.Finding {
	var out []health.Finding
	for _, f := range r.Findings {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func inLoopExecutor() config.AgentBinding {
	return config.AgentBinding{Role: config.RoleAgent, Command: "in-loop"}
}

func TestSharedProfileIsWarnedAndLocalProfileIsNot(t *testing.T) {
	global := localCatalogConfig(t)
	shared := config.AgentsConfig{
		Executor: inLoopExecutor(),
		Reviewer: config.AgentBinding{Role: config.RoleReviewer, Profile: "judge"},
		Agents:   map[string]config.AgentBinding{"summariser": {Role: config.RoleReviewer, Profile: "judge"}},
	}
	r := ValidateEffectiveLayered(config.AgentLayers{Repo: shared}, global, nil, nil, nil)
	fs := sharedFindings(r, health.IDAgentsSharedProfile)
	if len(fs) != 2 {
		t.Fatalf("want a warning per shared profile= binding, got %v", r.Findings)
	}
	for i, section := range []string{"[reviewer]", "[summariser]"} {
		f := fs[i]
		if f.Severity != health.SeverityWarn {
			t.Errorf("must be a warning, not a refusal: %v", f.Severity)
		}
		if !strings.Contains(f.Detail, section) || !strings.Contains(f.Detail, "agents.local.toml") {
			t.Errorf("must name the binding %s and agents.local.toml: %q", section, f.Detail)
		}
	}
	if !r.OK() {
		t.Errorf("a shared profile= is advisory, never a Problem: %v", r.Problems)
	}

	// The same profile written only in the local file: resolved, and no warning.
	clean := config.AgentsConfig{
		Executor: inLoopExecutor(),
		Reviewer: config.AgentBinding{Role: config.RoleReviewer},
	}
	local := config.AgentsConfig{Reviewer: config.AgentBinding{Profile: "judge"}}
	r = ValidateEffectiveLayered(config.AgentLayers{Repo: clean, Local: local}, global, nil, nil, nil)
	if fs := sharedFindings(r, health.IDAgentsSharedProfile); len(fs) != 0 {
		t.Errorf("a profile= only in agents.local.toml must not warn: %v", fs)
	}
	if got := r.Provenance.Source("reviewer", "model"); got != config.SourceProfile("judge") {
		t.Errorf("the local profile= must resolve: model sourced %q", got)
	}
}

func TestSharedUseGlobalRolesIsWarnedAndLocalIsNot(t *testing.T) {
	global := localCatalogConfig(t)
	base := config.AgentsConfig{Executor: inLoopExecutor(), Reviewer: config.AgentBinding{Role: config.RoleReviewer}}

	shared := base
	shared.Defaults.UseGlobalRoles = true
	r := ValidateEffectiveLayered(config.AgentLayers{Repo: shared}, global, nil, nil, nil)
	fs := sharedFindings(r, health.IDAgentsSharedGlobalRoles)
	if len(fs) != 1 || !strings.Contains(fs[0].Detail, "use_global_roles") || !strings.Contains(fs[0].Detail, "agents.local.toml") {
		t.Fatalf("want one warning naming use_global_roles and agents.local.toml, got %v", r.Findings)
	}

	local := config.AgentsConfig{Defaults: config.AgentsDefaults{UseGlobalRoles: true}}
	r = ValidateEffectiveLayered(config.AgentLayers{Repo: base, Local: local}, global, nil, nil, nil)
	if fs := sharedFindings(r, health.IDAgentsSharedGlobalRoles); len(fs) != 0 {
		t.Errorf("use_global_roles only in agents.local.toml must not warn: %v", fs)
	}
	if got := r.Provenance.Source("reviewer", "model"); got != config.SourceGlobalRole("judge") {
		t.Errorf("the local opt-in must reach the [roles] base: model sourced %q", got)
	}
}

// A seat declared ONLY in agents.local.toml and allocated by the route is
// resolved: no orphan advisory, no allocation error, provenance names local.
func TestLocalOnlySeatIsResolvedNotOrphaned(t *testing.T) {
	adv := advisorAgents()
	repo := config.AgentsConfig{Executor: adv.Executor, Reviewer: adv.Reviewer}
	local := config.AgentsConfig{Agents: map[string]config.AgentBinding{
		"park-triage":   adv.Agents["park-triage"],
		"lessons":       adv.Agents["lessons"],
		"never-alloced": adv.Agents["never-alloced"],
	}}
	r := ValidateEffectiveLayered(config.AgentLayers{Repo: repo, Local: local}, config.GlobalAgentsConfig{}, nil,
		routeDocs(advisorDone, advisorStep), nil)
	if !r.OK() {
		t.Fatalf("a local-only seat must not be a hard problem: %v", r.Problems)
	}
	for _, f := range r.Findings {
		if f.ID != health.IDNodeAlloc && !strings.Contains(f.Detail, "orphan") {
			continue
		}
		for _, name := range []string{"park-triage", "lessons"} {
			if strings.Contains(f.Detail, "["+name+"]") || strings.Contains(f.Detail, name) {
				t.Errorf("local-only seat %s reported as orphan/unknown: %q", name, f.Detail)
			}
		}
	}
	if got := r.Provenance.Source("lessons", "command"); got != config.SourceLocal {
		t.Errorf("a local-only seat must be sourced local, got %q", got)
	}
	// A local seat nothing references still warns, like any authored seat.
	var orphaned bool
	for _, w := range r.Warnings {
		orphaned = orphaned || (strings.Contains(w, "never-alloced") && strings.Contains(w, "orphan"))
	}
	if !orphaned {
		t.Errorf("an unreferenced local seat should still be advisory-orphaned: %v", r.Warnings)
	}
}

// The resolve-error fallback still judges the local layer, and names it.
func TestResolveErrorFallbackSeesLocalLayer(t *testing.T) {
	repo := config.AgentsConfig{Executor: inLoopExecutor(), Reviewer: config.AgentBinding{Role: config.RoleReviewer}}
	local := config.AgentsConfig{Reviewer: config.AgentBinding{Profile: "absent"}}
	r := ValidateEffectiveLayered(config.AgentLayers{Repo: repo, Local: local}, localCatalogConfig(t), nil, nil, nil)
	fs := sharedFindings(r, health.IDAgentsProfileBroken)
	if len(fs) != 1 || !strings.Contains(fs[0].Detail, "agents.local.toml") {
		t.Fatalf("a broken local profile must be reported naming agents.local.toml: %v", r.Findings)
	}
}
