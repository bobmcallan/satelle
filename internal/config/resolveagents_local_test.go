package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// localAgents parses an agents.local.toml body through the live loader path.
func localAgents(t testing.TB, body string) AgentsConfig {
	t.Helper()
	ac, err := loadAgentsBody(body)
	if err != nil {
		t.Fatalf("local layer: %v", err)
	}
	return ac
}

// A field the local file sets wins for the binding; a field it leaves alone
// resolves exactly as the repo's file says, and provenance names each tier.
func TestLocalTierOverridesFieldAndFallsThrough(t *testing.T) {
	repo := repoAgents(t, "[reviewer]\nrole = \"reviewer\"\nmodel = \"opus\"\ntools = \"Read,Grep\"\n")
	local := localAgents(t, "[reviewer]\nmodel = \"sonnet\"\ncommand = \"/opt/claude -p {system}\"\n")
	got, prov, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo, Local: local}, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewer.Model != "sonnet" || prov.Source("reviewer", "model") != SourceLocal {
		t.Errorf("local must win model: %q (%s)", got.Reviewer.Model, prov.Source("reviewer", "model"))
	}
	if got.Reviewer.Command != "/opt/claude -p {system}" || prov.Source("reviewer", "command") != SourceLocal {
		t.Errorf("local must supply command: %q (%s)", got.Reviewer.Command, prov.Source("reviewer", "command"))
	}
	if got.Reviewer.Tools != "Read,Grep" || prov.Source("reviewer", "tools") != SourceRepo {
		t.Errorf("an unset local field must fall through to the repo: %q (%s)", got.Reviewer.Tools, prov.Source("reviewer", "tools"))
	}
}

// With no local file, resolution is byte-identical to the layers without it —
// whether the file is absent or present-and-empty.
func TestLocalTierAbsentResolvesAsBefore(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	body := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\nmodel = \"opus\"\n\n[coder]\nrole = \"agent\"\nmodel = \"x\"\n"
	if err := os.MkdirAll(filepath.Join(dir, AgentsConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AgentsRel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	absent, err := LoadEffectiveAgents(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := BaselineAgents()
	if err != nil {
		t.Fatal(err)
	}
	want, wantProv, err := ResolveAgentsBaseline(baseline, AgentLayers{Repo: repoAgents(t, body)}, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(absent.Agents, want) || !reflect.DeepEqual(absent.Provenance, wantProv) {
		t.Errorf("an absent local file must not change resolution:\n got %+v\nwant %+v", absent.Agents, want)
	}
	if err := os.WriteFile(LocalAgentsPath(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty, err := LoadEffectiveAgents(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(empty.Agents, want) || !reflect.DeepEqual(empty.Provenance, wantProv) {
		t.Error("an empty local file must not change resolution")
	}
}

// A profile= written only in the local file resolves from the machine catalog,
// and the local profile wins over the one the shared file names.
func TestLocalProfileResolvesAndBeatsSharedProfile(t *testing.T) {
	cat := catalog(t, twoProfileCatalog)
	repo := repoAgents(t, "[reviewer]\nrole = \"reviewer\"\n")
	local := localAgents(t, "[reviewer]\nprofile = \"claude-opus\"\n")
	got, prov, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo, Local: local}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewer.Model != "opus" || prov.Source("reviewer", "model") != SourceProfile("claude-opus") {
		t.Errorf("a local-only profile= must resolve: model %q (%s)", got.Reviewer.Model, prov.Source("reviewer", "model"))
	}

	shared := repoAgents(t, "[reviewer]\nrole = \"reviewer\"\nprofile = \"grok-acp\"\n")
	got, prov, err = ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: shared, Local: local}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if prov.Source("reviewer", "model") != SourceProfile("claude-opus") || got.Reviewer.Profile != "claude-opus" {
		t.Errorf("local profile= must beat the shared one: %+v", prov["reviewer"])
	}

	// A broken local profile names agents.local.toml, not the shared file.
	_, _, err = ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo, Local: localAgents(t, "[reviewer]\nprofile = \"absent\"\n")}, cat)
	if err == nil || !strings.Contains(err.Error(), LocalAgentsName) {
		t.Errorf("a broken local profile must name %s, got %v", LocalAgentsName, err)
	}
}

// Role is identity: a local role that disagrees with the shared binding's, or
// with its profile's, is refused naming the local file.
func TestLocalRoleDisagreementIsRefused(t *testing.T) {
	cat := catalog(t, twoProfileCatalog)
	for name, c := range map[string]struct{ repo, local string }{
		"against the shared binding": {"[summariser]\nrole = \"reviewer\"\n", "[summariser]\nrole = \"agent\"\n"},
		"against the profile":        {"[summariser]\nprofile = \"claude-opus\"\n", "[summariser]\nrole = \"agent\"\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repoAgents(t, c.repo), Local: localAgents(t, c.local)}, cat)
			if err == nil || !strings.Contains(err.Error(), LocalAgentsName) || !strings.Contains(err.Error(), "role is identity") {
				t.Errorf("want a role-is-identity error naming %s, got %v", LocalAgentsName, err)
			}
		})
	}
	if _, _, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{
		Repo: repoAgents(t, "[summariser]\nrole = \"reviewer\"\n"), Local: localAgents(t, "[summariser]\nrole = \"reviewer\"\n")}, cat); err != nil {
		t.Errorf("restating the same role is fine: %v", err)
	}
}

// Local [defaults] override the shared ones key by key: a key set only in the
// local file applies, and a key set only in the shared file still applies.
func TestLocalDefaultsOverrideKeyByKey(t *testing.T) {
	cat := catalog(t, `
[profiles.role-default]
role    = "reviewer"
command = "role-cmd {system}"
model   = "role-model"

[roles]
reviewer = "role-default"
`)
	repo := repoAgents(t, "[defaults]\nidle_timeout = \"7m\"\nsecondary = \"fallback\"\n\n[reviewer]\nrole = \"reviewer\"\n")
	local := localAgents(t, "[defaults]\nuse_global_roles = true\nsecondary = \"mine\"\n")
	got, prov, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo, Local: local}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewer.Model != "role-model" || prov.Source("reviewer", "model") != SourceGlobalRole("role-default") {
		t.Errorf("use_global_roles set only locally must enable the [roles] base: %q (%s)", got.Reviewer.Model, prov.Source("reviewer", "model"))
	}
	if got.Defaults.IdleTimeout != "7m" {
		t.Errorf("a key set only in the shared file must still apply: %+v", got.Defaults)
	}
	if got.Defaults.Secondary != "mine" {
		t.Errorf("a key set in both must take the local value: %+v", got.Defaults)
	}
	// Without the local opt-in the same shared file reaches no catalog role.
	got, _, err = ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Repo: repo}, cat)
	if err != nil || got.Reviewer.Model != "" {
		t.Errorf("no opt-in must mean no [roles] base: %q, %v", got.Reviewer.Model, err)
	}
}

// A seat declared only in the local file is resolved, attributed wholly to it.
func TestLocalOnlySeatResolves(t *testing.T) {
	local := localAgents(t, "[scratch]\nrole = \"agent\"\ncommand = \"in-loop\"\nmodel = \"m\"\n")
	got, prov, err := ResolveAgentsBaseline(AgentsConfig{}, AgentLayers{Local: local}, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := got.Agents["scratch"]; !ok || b.Model != "m" || prov.Source("scratch", "model") != SourceLocal {
		t.Errorf("a local-only seat must resolve: %+v %v", got.Agents, prov["scratch"])
	}
}

// LoadAgentLayers reads all three layers, reports a bad one by name and file,
// and still returns the layers that loaded.
func TestLoadAgentLayers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, AgentsConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(AgentsRel, "[reviewer]\nmodel = \"repo\"\n")
	l, err := LoadAgentLayers(dir)
	if err != nil || l.Repo.Reviewer.Model != "repo" || len(l.Local.Agents) != 0 {
		t.Fatalf("absent local = zero layer: %+v, %v", l, err)
	}
	write(LocalAgentsRel, "[reviewer]\nmodel = \"local\"\n")
	if l, err = LoadAgentLayers(dir); err != nil || l.Local.Reviewer.Model != "local" {
		t.Fatalf("local layer not loaded: %+v, %v", l, err)
	}
	if got := l.Authored().Reviewer.Model; got != "local" {
		t.Errorf("Authored must put local over repo: %q", got)
	}
	write(LocalAgentsRel, "[reviewer\n")
	l, err = LoadAgentLayers(dir)
	if err == nil || l.LocalErr == nil || !strings.Contains(err.Error(), LocalAgentsRel) {
		t.Errorf("a malformed local file must be reported by name: %v", err)
	}
	if l.Repo.Reviewer.Model != "repo" || l.RepoErr != nil {
		t.Errorf("the other layers still load: %+v", l)
	}
}

func TestSourceFile(t *testing.T) {
	for label, want := range map[string]string{
		SourceLocal:                    ".satelle/workflows/agents.local.toml",
		SourceRepo:                     ".satelle/workflows/agents.toml",
		SourceWorkspace:                ".satelle/workflows/agents.workspace.toml",
		SourceProfile("p"):             GlobalAgentsLabel,
		SourceGlobalRole("p"):          GlobalAgentsLabel,
		SourceBaseline:                 "embedded",
		SourceEmbedded:                 "embedded",
		SourceLocal + "+" + SourceRepo: ".satelle/workflows/agents.local.toml + .satelle/workflows/agents.toml",
	} {
		if got := SourceFile(label); got != want {
			t.Errorf("SourceFile(%q) = %q, want %q", label, got, want)
		}
	}
}

// agents.local.toml never leaves the machine: LocalOnlyPath withholds it, and a
// push bundle built from a tree holding it omits the file (listed as withheld).
func TestLocalAgentsFileNeverSyncs(t *testing.T) {
	if !LocalOnlyPath("workflows/agents.local.toml") {
		t.Fatal("workflows/agents.local.toml must be local-only")
	}
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/workflows/agents.toml", "[executor]\ncommand = \"in-loop\"\n")
	writeFile(t, repo, ".satelle/workflows/agents.local.toml", "[reviewer]\nprofile = \"mine\"\n")
	b, err := ConfigFiles(Config{}, TeamScope, repo, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range b.Files {
		if strings.Contains(f.Path, LocalAgentsName) {
			t.Errorf("push bundle carries %s", f.Path)
		}
	}
	if _, ok := findConfigFile(b.Files, AgentsRel); !ok {
		t.Error("the shared agents.toml must still sync")
	}
}
