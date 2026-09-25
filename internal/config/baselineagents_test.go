package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

const baselineCatalog = `
[profiles.claude-agent]
role    = "agent"
command = "claude -p --model {model}"
tools   = "Read,Grep"
model   = "sonnet"

[profiles.claude-reviewer]
role    = "reviewer"
command = "claude -p --output-format json --model {model}"
tools   = "Read,Grep,Glob"
model   = "opus"

[profiles.grok-reviewer]
role    = "reviewer"
command = "grok --model {model}"
tools   = "read_file"
model   = "grok-4"

[roles]
agent    = "claude-agent"
reviewer = "claude-reviewer"
`

func baselineOrFail(t *testing.T) AgentsConfig {
	t.Helper()
	b, err := BaselineAgents()
	if err != nil {
		t.Fatalf("BaselineAgents: %v", err)
	}
	if len(b.Agents) == 0 {
		t.Fatal("baseline carries no seats")
	}
	return b
}

// AC1: a repo file that names one seat leaves every other baseline seat
// unchanged, and adds or drops none.
func TestBaselineRepoOverridesOneSeat(t *testing.T) {
	base := baselineOrFail(t)
	cat := catalog(t, baselineCatalog)
	repo := repoAgents(t, "[reviewer]\nprofile = \"grok-reviewer\"\n")

	plain, plainProv, err := ResolveAgentsBaseline(base, AgentsConfig{}, AgentsConfig{}, cat)
	if err != nil {
		t.Fatal(err)
	}
	over, overProv, err := ResolveAgentsBaseline(base, repo, AgentsConfig{}, cat)
	if err != nil {
		t.Fatal(err)
	}

	names := func(a AgentsConfig) []string {
		var out []string
		for n := range a.Agents {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(names(over), names(plain)) || !reflect.DeepEqual(names(plain), names(base)) {
		t.Fatalf("seat set changed: base %v plain %v over %v", names(base), names(plain), names(over))
	}
	for n := range plain.Agents {
		if !reflect.DeepEqual(plain.Agents[n], over.Agents[n]) || !reflect.DeepEqual(plainProv[n], overProv[n]) {
			t.Errorf("seat %q changed though the repo did not name it", n)
		}
	}
	if !reflect.DeepEqual(plain.Executor, over.Executor) {
		t.Error("executor changed though the repo did not name it")
	}
	for _, f := range []string{"command", "tools", "model"} {
		if got := overProv.Source("reviewer", f); got != "profile:grok-reviewer" {
			t.Errorf("reviewer %s source = %q, want profile:grok-reviewer", f, got)
		}
	}
}

// AC2: with no repo file the baseline seats are the ones that run.
func TestBaselineNoRepoFile(t *testing.T) {
	home := testutil.IsolateHome(t)
	_ = home
	dataDir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := baselineOrFail(t)
	eff, err := LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for n, b := range base.Agents {
		got, ok := eff.Agents.Agents[n]
		if !ok {
			t.Fatalf("baseline seat %q not resolved", n)
		}
		if got.Role != b.Role || got.Principles != b.Principles {
			t.Errorf("seat %q = %+v, want baseline %+v", n, got, b)
		}
	}
	if len(eff.Agents.Agents) != len(base.Agents) {
		t.Errorf("resolved %d seats, baseline has %d", len(eff.Agents.Agents), len(base.Agents))
	}
	if eff.Agents.ExecutorBinding().Command != "in-loop" {
		t.Errorf("executor command = %q, want in-loop", eff.Agents.ExecutorBinding().Command)
	}
	if got, ok := eff.Agents.NamedBinding("planner"); !ok || got.Role != RoleAgent {
		t.Errorf("planner = %+v ok=%v", got, ok)
	}
}

// AC3: a seat's command, tools and model resolve from a machine profile with no
// repo restatement — through [roles] for an unnamed seat, through profile= for a
// seat the repo points at a profile.
func TestBaselineSeatResolvesFromProfile(t *testing.T) {
	base := baselineOrFail(t)
	cat := catalog(t, baselineCatalog)

	// Opt-in is the only path from [roles] onto a seat that names no profile,
	// including a seat that exists only on the baseline.
	optIn := repoAgents(t, "[defaults]\nuse_global_roles = true\n")
	agents, prov, err := ResolveAgentsBaseline(base, optIn, AgentsConfig{}, cat)
	if err != nil {
		t.Fatal(err)
	}
	for seat, want := range map[string]string{"planner": "global-role:claude-agent", "reviewer-summary": "global-role:claude-reviewer"} {
		b := agents.Agents[seat]
		if b.Command == "" || b.Tools == "" || b.Model == "" {
			t.Errorf("%s did not resolve command/tools/model from the profile: %+v", seat, b)
		}
		for _, f := range []string{"command", "tools", "model"} {
			if got := prov.Source(seat, f); got != want {
				t.Errorf("%s %s source = %q, want %s", seat, f, got, want)
			}
		}
	}
	if agents.Reviewer.Model != "opus" {
		t.Errorf("reviewer model = %q, want opus from claude-reviewer", agents.Reviewer.Model)
	}

	repo := repoAgents(t, "[planner]\nprofile = \"claude-agent\"\n")
	agents, prov, err = ResolveAgentsBaseline(base, repo, AgentsConfig{}, cat)
	if err != nil {
		t.Fatal(err)
	}
	p := agents.Agents["planner"]
	if p.Command == "" || p.Model != "sonnet" || p.Role != RoleAgent || p.Principles != "session" {
		t.Errorf("planner = %+v", p)
	}
	if prov.Source("planner", "command") != "profile:claude-agent" || prov.Source("planner", "role") != SourceBaseline {
		t.Errorf("planner provenance = %v", prov["planner"])
	}
}

// A repo that sets a role differing from the baseline seat is refused.
func TestBaselineRoleConflictRefused(t *testing.T) {
	base := baselineOrFail(t)
	repo := repoAgents(t, "[planner]\nrole = \"reviewer\"\n")
	if _, _, err := ResolveAgentsBaseline(base, repo, AgentsConfig{}, GlobalAgentsConfig{}); err == nil {
		t.Fatal("want role-identity refusal against the baseline")
	}
}

// A profile exists for each role on each supported CLI.
func TestStarterGlobalAgentsHasAgentAndReviewerProfiles(t *testing.T) {
	for _, cli := range []string{"claude", "grok", "codex"} {
		body, err := StarterGlobalAgents(cli)
		if err != nil {
			t.Fatalf("%s: %v", cli, err)
		}
		g, err := ParseGlobalAgents(body)
		if err != nil {
			t.Fatalf("%s: starter does not parse: %v", cli, err)
		}
		for _, p := range []string{cli + "-agent", cli + "-reviewer"} {
			if _, ok := g.Profiles[p]; !ok {
				t.Errorf("%s starter missing profile %q", cli, p)
			}
		}
	}
}
