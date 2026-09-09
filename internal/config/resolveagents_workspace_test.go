package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// wsAgents parses a workspace-layer body through the live loader path.
func wsAgents(t testing.TB, body string) AgentsConfig {
	t.Helper()
	ac, err := loadAgentsBody(body)
	if err != nil {
		t.Fatalf("workspace layer: %v", err)
	}
	return ac
}

// TestWorkspaceTierRepoWinsFieldByField (AC2 a/c): a repo table and a workspace
// table of the same name merge field by field — the repo's scalar wins, a
// workspace scalar fills a repo blank — and provenance names each source.
func TestWorkspaceTierRepoWinsFieldByField(t *testing.T) {
	repo := repoAgents(t, `
[reviewer]
role  = "reviewer"
model = "opus"
`)
	ws := wsAgents(t, `
[reviewer]
role    = "reviewer"
command = "claude -p {system} --model {model}"
tools   = "Read,Grep"
model   = "sonnet"
`)
	got, prov, err := ResolveAgentsLayered(repo, ws, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	rb := got.Reviewer
	if rb.Model != "opus" {
		t.Errorf("repo scalar must win: model = %q", rb.Model)
	}
	if rb.Tools != "Read,Grep" || !strings.Contains(rb.Command, "{system}") {
		t.Errorf("workspace must fill repo blanks: %+v", rb)
	}
	if s := prov.Source("reviewer", "model"); s != SourceRepo {
		t.Errorf("provenance model = %q, want %q", s, SourceRepo)
	}
	for _, f := range []string{"tools", "command"} {
		if s := prov.Source("reviewer", f); s != SourceWorkspace {
			t.Errorf("provenance %s = %q, want %q", f, s, SourceWorkspace)
		}
	}
}

// TestWorkspaceOnlySectionApplies (AC2 b): a binding the repo never declared
// but the workspace publishes is present, wholly attributed to the workspace.
func TestWorkspaceOnlySectionApplies(t *testing.T) {
	repo := repoAgents(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
	ws := wsAgents(t, `
[coder]
role      = "agent"
interface = "stream"
command   = "claude -p --input-format stream-json --output-format stream-json --allowedTools {tools}"
tools     = "Read,Edit"
`)
	got, prov, err := ResolveAgentsLayered(repo, ws, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := got.Agents["coder"]
	if !ok || c.Interface != "stream" || c.Tools != "Read,Edit" {
		t.Fatalf("workspace-only binding must apply: %+v (ok=%v)", c, ok)
	}
	for _, f := range []string{"command", "tools", "interface", "role"} {
		if s := prov.Source("coder", f); s != SourceWorkspace {
			t.Errorf("provenance coder.%s = %q, want %q", f, s, SourceWorkspace)
		}
	}
}

// TestWorkspaceWinsOverProfile (AC2 d): the ladder is repo → workspace →
// profile. A workspace value beats the catalog profile the repo referenced,
// while fields the workspace leaves blank still come from the profile.
func TestWorkspaceWinsOverProfile(t *testing.T) {
	repo := repoAgents(t, `
[reviewer]
profile = "claude-opus"
`)
	ws := wsAgents(t, `
[reviewer]
model = "sonnet"
`)
	got, prov, err := ResolveAgentsLayered(repo, ws, catalog(t, twoProfileCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewer.Model != "sonnet" {
		t.Errorf("workspace must beat the profile: model = %q", got.Reviewer.Model)
	}
	if !strings.Contains(got.Reviewer.Command, "{system}") || got.Reviewer.Tools != "Read,Grep,Glob" {
		t.Errorf("profile must still fill what the workspace left blank: %+v", got.Reviewer)
	}
	if s := prov.Source("reviewer", "model"); s != SourceWorkspace {
		t.Errorf("provenance model = %q, want %q", s, SourceWorkspace)
	}
	if s := prov.Source("reviewer", "command"); s != SourceProfile("claude-opus") {
		t.Errorf("provenance command = %q, want profile", s)
	}
}

// TestNoWorkspaceLayerIsByteIdentical (AC2 e / AC5): with no workspace layer
// the layered resolver returns exactly what the unlayered one does — config
// and provenance alike — so every repo that never pulled one is unchanged.
func TestNoWorkspaceLayerIsByteIdentical(t *testing.T) {
	repo := repoAgents(t, `
[reviewer]
profile = "claude-opus"
effort  = "low"

[coder]
role    = "agent"
command = "claude -p {system}"
`)
	g := catalog(t, twoProfileCatalog)
	a1, p1, err1 := ResolveAgents(repo, g)
	a2, p2, err2 := ResolveAgentsLayered(repo, AgentsConfig{}, g)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !reflect.DeepEqual(a1, a2) || !reflect.DeepEqual(p1, p2) {
		t.Fatalf("zero workspace layer must be a no-op:\n%+v\n%+v\n%+v\n%+v", a1, a2, p1, p2)
	}
}

// TestWorkspaceRoleIdentityConflictRefused: role is identity for the workspace
// tier as it is for a profile — a disagreement is an error, not a merge.
func TestWorkspaceRoleIdentityConflictRefused(t *testing.T) {
	repo := repoAgents(t, "[summary]\nrole = \"reviewer\"\n")
	ws := wsAgents(t, "[summary]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\n")
	_, _, err := ResolveAgentsLayered(repo, ws, GlobalAgentsConfig{})
	if err == nil || !strings.Contains(err.Error(), WorkspaceAgentsRel) {
		t.Fatalf("want role conflict naming the workspace layer, got %v", err)
	}
}

// TestWorkspaceLayerVarsResolveLocally (AC3): a ${VAR} a workspace binding
// declares expands from THIS machine's [vars] at wiring time, and an unknown
// var fails fast — the workspace publishes the key, the machine supplies the value.
func TestWorkspaceLayerVarsResolveLocally(t *testing.T) {
	repo := repoAgents(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n")
	ws := wsAgents(t, "[coder]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\nenv = { ANTHROPIC_AUTH_TOKEN = \"${LOCAL_TOKEN}\" }\n")
	got, _, err := ResolveAgentsLayered(repo, ws, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := ResolveAgentEnvs(got, map[string]string{"LOCAL_TOKEN": "machine-value"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents["coder"].Env["ANTHROPIC_AUTH_TOKEN"] != "machine-value" {
		t.Errorf("workspace ${VAR} must resolve from local vars: %+v", res.Agents["coder"].Env)
	}
	if _, err := ResolveAgentEnvs(got, nil); err == nil || !strings.Contains(err.Error(), "LOCAL_TOKEN") {
		t.Fatalf("unknown var must fail fast naming the key, got %v", err)
	}
}

// TestWorkspaceBlankValuesNeverShadowTheMachine (AC3, code-ac review): a pulled
// layer arrives redacted — literal env values blank, ${VAR} references kept.
// A blank on the workspace tier is dropped from the effective binding, so it
// can never lay `KEY=` over the machine's real environment or a blank secret
// over settings.local.json; a reference resolves from the local [vars] and
// fails fast when the machine lacks it.
func TestWorkspaceBlankValuesNeverShadowTheMachine(t *testing.T) {
	repo := repoAgents(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\n")
	// Exactly what ingest produces for a binding that had a literal token, a
	// reference, and secret settings upstream.
	ws := wsAgents(t, `
[reviewer]
role     = "reviewer"
command  = "claude -p {system}"
env      = { ANTHROPIC_AUTH_TOKEN = "", ANTHROPIC_BASE_URL = "${BASE_URL}" }
settings = { model = "opus", apiKey = "", env = { CLAUDE_TOKEN = "" } }

[coder]
role    = "agent"
command = "claude -p {system}"
env     = { ONLY_BLANK = "" }
`)
	got, prov, err := ResolveAgentsLayered(repo, ws, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	rb := got.Reviewer
	if _, shadowed := rb.Env["ANTHROPIC_AUTH_TOKEN"]; shadowed {
		t.Fatalf("a workspace blank must not become KEY= in the effective env: %+v", rb.Env)
	}
	if rb.Env["ANTHROPIC_BASE_URL"] != "${BASE_URL}" {
		t.Errorf("a ${VAR} reference must be carried for local resolution: %+v", rb.Env)
	}
	if _, ok := rb.Settings["apiKey"]; ok {
		t.Errorf("a blank secret setting must not ride into --settings: %+v", rb.Settings)
	}
	if _, ok := rb.Settings["env"]; ok {
		t.Errorf("an all-blank settings.env must not ride into --settings: %+v", rb.Settings)
	}
	if rb.Settings["model"] != "opus" {
		t.Errorf("real workspace settings still apply: %+v", rb.Settings)
	}
	if got.Agents["coder"].Env != nil {
		t.Errorf("an env of only blanks must contribute nothing: %+v", got.Agents["coder"].Env)
	}
	if s := prov.Source("reviewer", "env"); s != SourceWorkspace {
		t.Errorf("the reference the workspace supplied is attributed to it: %q", s)
	}
	// Local resolution of the reference: present → value; absent → fail fast.
	res, err := ResolveAgentEnvs(got, map[string]string{"BASE_URL": "https://local.example"})
	if err != nil || res.Reviewer.Env["ANTHROPIC_BASE_URL"] != "https://local.example" {
		t.Fatalf("reference must resolve from local vars: %+v %v", res.Reviewer.Env, err)
	}
	if _, err := ResolveAgentEnvs(got, nil); err == nil || !strings.Contains(err.Error(), "BASE_URL") {
		t.Fatalf("a missing local var must fail fast naming it, got %v", err)
	}
}

// TestLoadWorkspaceAgentsAndEffectiveLadder: the on-disk layer file is read by
// LoadEffectiveAgents beside agents.toml — absent is the zero layer, malformed
// is loud, present is merged with workspace provenance.
func TestLoadWorkspaceAgentsAndEffectiveLadder(t *testing.T) {
	testutil.IsolateHome(t)
	dataDir := t.TempDir()
	wfDir := filepath.Join(dataDir, AgentsConfigDir)
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, AgentsConfigName), []byte("[reviewer]\nrole = \"reviewer\"\nmodel = \"opus\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ws, err := LoadWorkspaceAgents(dataDir); err != nil || len(ws.Agents) != 0 || ws.Reviewer.Model != "" {
		t.Fatalf("absent layer must be zero + nil: %+v %v", ws, err)
	}
	if err := os.WriteFile(WorkspaceAgentsPath(dataDir), []byte("[reviewer\nbroken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkspaceAgents(dataDir); err == nil || !strings.Contains(err.Error(), WorkspaceAgentsRel) {
		t.Fatalf("malformed layer must fail loud naming the file, got %v", err)
	}
	if err := os.WriteFile(WorkspaceAgentsPath(dataDir), []byte("[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\nmodel = \"sonnet\"\n\n[coder]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eff, err := LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Agents.Reviewer.Model != "opus" || !strings.Contains(eff.Agents.Reviewer.Command, "{system}") {
		t.Errorf("effective reviewer = %+v", eff.Agents.Reviewer)
	}
	if _, ok := eff.Agents.Agents["coder"]; !ok {
		t.Error("workspace-only coder missing from the effective layer")
	}
	if s := eff.Provenance.Source("reviewer", "command"); s != SourceWorkspace {
		t.Errorf("provenance command = %q, want workspace", s)
	}
	if s := eff.Provenance.Source("reviewer", "model"); s != SourceRepo {
		t.Errorf("provenance model = %q, want repo", s)
	}
}
