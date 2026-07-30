package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// catalog parses a catalog body or fails the test — the fixture helper the
// precedence cases share.
func catalog(t testing.TB, body string) GlobalAgentsConfig {
	t.Helper()
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatalf("ParseGlobalAgents: %v", err)
	}
	return g
}

// repoAgents writes an agents.toml into a fresh data dir and loads it, so the
// precedence cases exercise the real repo loader rather than a hand-built struct.
func repoAgents(t testing.TB, body string) AgentsConfig {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	return ac
}

const twoProfileCatalog = `
[profiles.claude-opus]
role      = "reviewer"
interface = "command"
command   = "claude -p --append-system-prompt {system} --model {model}"
tools     = "Read,Grep,Glob"
model     = "opus"
effort    = "high"
timeout   = "45m"
env       = { ANTHROPIC_AUTH_TOKEN = "${TOKEN}", KEEP = "base" }

[profiles.grok-acp]
role      = "reviewer"
interface = "acp"
command   = "grok agent stdio"
tools     = "read_file,grep,list_dir"
model     = "grok-4.5"
`

// TestResolveReferencedProfileFillsAndRepoOverrides pins AC2: a binding that
// names a profile inherits its execution fields, and any non-identity field the
// repo states inline wins. Maps merge key-wise with the repo's key winning.
func TestResolveReferencedProfileFillsAndRepoOverrides(t *testing.T) {
	repo := repoAgents(t, `
[reviewer]
profile = "claude-opus"
effort  = "low"
env     = { KEEP = "repo-wins", EXTRA = "repo-only" }
`)
	got, prov, err := ResolveAgents(repo, catalog(t, twoProfileCatalog))
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	rb := got.Reviewer
	if !strings.Contains(rb.Command, "{system}") {
		t.Errorf("command not inherited from the profile: %q", rb.Command)
	}
	if rb.Model != "opus" || rb.Tools != "Read,Grep,Glob" || rb.Timeout != "45m" {
		t.Errorf("profile fields not inherited: %+v", rb)
	}
	if rb.Effort != "low" {
		t.Errorf("repo effort must win over the profile's: %q", rb.Effort)
	}
	if rb.Env["KEEP"] != "repo-wins" || rb.Env["EXTRA"] != "repo-only" || rb.Env["ANTHROPIC_AUTH_TOKEN"] != "${TOKEN}" {
		t.Errorf("env must merge key-wise with the repo key winning: %+v", rb.Env)
	}
	// Provenance describes the binding it returned, field by field.
	want := map[string]string{
		"command": SourceProfile("claude-opus"),
		"model":   SourceProfile("claude-opus"),
		"tools":   SourceProfile("claude-opus"),
		"effort":  SourceRepo,
	}
	for field, src := range want {
		if got := prov.Source("reviewer", field); got != src {
			t.Errorf("provenance[reviewer][%s] = %q, want %q", field, got, src)
		}
	}
	if src := prov.Source("reviewer", "env"); !strings.Contains(src, SourceRepo) || !strings.Contains(src, "claude-opus") {
		t.Errorf("a merged map must name both contributing tiers, got %q", src)
	}
}

// TestResolveSameNameProfileDoesNotMerge is the CRITICAL negative for AC3: a
// catalog profile that merely shares a binding's name is inert. The repo's
// bindings resolve byte-identically with and without the catalog present, so
// adding a machine-wide profile can never silently re-point a pinned repo.
func TestResolveSameNameProfileDoesNotMerge(t *testing.T) {
	repo := repoAgents(t, `
[reviewer]
role    = "reviewer"
command = "claude -p {system}"
model   = "sonnet"
`)
	withoutCatalog, _, err := ResolveAgents(repo, GlobalAgentsConfig{})
	if err != nil {
		t.Fatalf("ResolveAgents (no catalog): %v", err)
	}
	// A catalog whose profile is named exactly like the binding, and a [roles]
	// default for the same role — neither is referenced, so neither applies.
	hostile := catalog(t, `
[profiles.reviewer]
role    = "reviewer"
command = "hijacked -p {system}"
model   = "hijack"

[roles]
reviewer = "reviewer"
`)
	withCatalog, prov, err := ResolveAgents(repo, hostile)
	if err != nil {
		t.Fatalf("ResolveAgents (catalog): %v", err)
	}
	if !reflect.DeepEqual(withoutCatalog, withCatalog) {
		t.Fatalf("an unreferenced catalog must not change resolution:\n without: %+v\n with:    %+v",
			withoutCatalog, withCatalog)
	}
	if withCatalog.Reviewer.Model != "sonnet" {
		t.Errorf("repo model was overwritten by a same-name profile: %q", withCatalog.Reviewer.Model)
	}
	for _, f := range []string{"command", "model"} {
		if src := prov.Source("reviewer", f); src != SourceRepo {
			t.Errorf("provenance[reviewer][%s] = %q, want %q", f, src, SourceRepo)
		}
	}
}

// TestResolvePrecedenceLadder pins AC3's documented order by asserting the
// winner at each tier for the same field, with the tier below still supplying
// what the tier above leaves blank.
func TestResolvePrecedenceLadder(t *testing.T) {
	cat := catalog(t, `
[profiles.pinned]
role    = "reviewer"
command = "profile-cmd {system}"
model   = "profile-model"

[profiles.role-default]
role    = "reviewer"
command = "role-cmd {system}"
model   = "role-model"

[roles]
reviewer = "role-default"
`)
	cases := []struct {
		name       string
		repoBody   string
		wantModel  string
		wantSource string
	}{
		{
			name:       "tier 1 — repo inline wins over the profile it references",
			repoBody:   "[defaults]\nuse_global_roles = true\n\n[reviewer]\nprofile = \"pinned\"\nmodel = \"repo-model\"\n",
			wantModel:  "repo-model",
			wantSource: SourceRepo,
		},
		{
			name:       "tier 2 — the explicitly referenced profile beats the role default",
			repoBody:   "[defaults]\nuse_global_roles = true\n\n[reviewer]\nprofile = \"pinned\"\n",
			wantModel:  "profile-model",
			wantSource: SourceProfile("pinned"),
		},
		{
			name:       "tier 3 — the opt-in role default applies when nothing is referenced",
			repoBody:   "[defaults]\nuse_global_roles = true\n\n[reviewer]\nrole = \"reviewer\"\n",
			wantModel:  "role-model",
			wantSource: SourceGlobalRole("role-default"),
		},
		{
			name:       "tier 3 is skipped entirely without the opt-in",
			repoBody:   "[reviewer]\nrole = \"reviewer\"\n",
			wantModel:  "",
			wantSource: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, prov, err := ResolveAgents(repoAgents(t, c.repoBody), cat)
			if err != nil {
				t.Fatalf("ResolveAgents: %v", err)
			}
			if got.Reviewer.Model != c.wantModel {
				t.Errorf("model = %q, want %q", got.Reviewer.Model, c.wantModel)
			}
			if src := prov.Source("reviewer", "model"); src != c.wantSource {
				t.Errorf("source = %q, want %q", src, c.wantSource)
			}
		})
	}
}

// TestResolveEmbeddedFallbackIsTierFour pins the bottom of the ladder: with no
// repo value and no catalog reference, the binary's embedded defaults still
// apply and are attributed as such, so a display never shows an unsourced value.
func TestResolveEmbeddedFallbackIsTierFour(t *testing.T) {
	got, prov, err := ResolveAgents(repoAgents(t, "[reviewer]\nrole = \"reviewer\"\n"), GlobalAgentsConfig{})
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	if prov.Source("reviewer", "command") != SourceEmbedded || prov.Source("reviewer", "tools") != SourceEmbedded {
		t.Errorf("unfilled reviewer fields must be attributed to the embedded fallback: %+v", prov["reviewer"])
	}
	if rb := got.ReviewerBinding(); rb.Command != DefaultReviewerCommand || rb.Tools != DefaultReviewerTools {
		t.Errorf("embedded defaults must still resolve: %+v", rb)
	}
	if prov.Source("executor", "command") != SourceEmbedded {
		t.Errorf("executor command must be attributed to the embedded fallback: %+v", prov["executor"])
	}
}

// TestResolveInlineRepoIsUnchangedByTheCatalog pins AC2's second half and AC6's
// compat guarantee: a fully inline repo — the shape every existing installation
// has — resolves identically whether or not a catalog exists on the machine.
func TestResolveInlineRepoIsUnchangedByTheCatalog(t *testing.T) {
	repo := repoAgents(t, `
[executor]
command = "in-loop"
role    = "agent"

[reviewer]
role    = "reviewer"
command = "claude -p --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "sonnet"

[planner]
role    = "agent"
command = "claude -p --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob,Bash(satelle:*)"
model   = "opus"
`)
	bare, _, err := ResolveAgents(repo, GlobalAgentsConfig{})
	if err != nil {
		t.Fatalf("ResolveAgents (no catalog): %v", err)
	}
	full, _, err := ResolveAgents(repo, catalog(t, twoProfileCatalog))
	if err != nil {
		t.Fatalf("ResolveAgents (catalog): %v", err)
	}
	if !reflect.DeepEqual(bare, full) {
		t.Errorf("an inline repo must be untouched by the catalog:\n bare: %+v\n full: %+v", bare, full)
	}
}

// TestResolveRefusesBrokenReferences pins AC4's resolution-time refusals: a
// binding naming a profile the catalog does not define, and a repo whose
// declared role contradicts the profile's, are both hard errors.
func TestResolveRefusesBrokenReferences(t *testing.T) {
	_, _, err := ResolveAgents(repoAgents(t, "[reviewer]\nprofile = \"absent\"\n"), catalog(t, twoProfileCatalog))
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Errorf("a missing profile must be refused by name: %v", err)
	}
	_, _, err = ResolveAgents(repoAgents(t, "[summariser]\nprofile = \"claude-opus\"\nrole = \"agent\"\n"), catalog(t, twoProfileCatalog))
	if err == nil || !strings.Contains(err.Error(), "role") {
		t.Errorf("a repo/profile role conflict must be refused: %v", err)
	}
	// Restating the SAME role is not a conflict.
	if _, _, err := ResolveAgents(repoAgents(t, "[summariser]\nprofile = \"claude-opus\"\nrole = \"reviewer\"\n"),
		catalog(t, twoProfileCatalog)); err != nil {
		t.Errorf("restating the profile's role must be accepted: %v", err)
	}
}

// TestResolveProfileChain pins the transitive case: a profile extending another
// resolves outermost-first, and provenance names the profile each field came
// from rather than collapsing the chain to its head.
func TestResolveProfileChain(t *testing.T) {
	cat := catalog(t, `
[profiles.base]
role    = "reviewer"
command = "claude -p {system}"
tools   = "Read,Grep,Glob"
model   = "sonnet"

[profiles.strong]
profile = "base"
model   = "opus"
`)
	got, prov, err := ResolveAgents(repoAgents(t, "[reviewer]\nprofile = \"strong\"\n"), cat)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	if got.Reviewer.Model != "opus" || got.Reviewer.Tools != "Read,Grep,Glob" {
		t.Errorf("chain not folded outermost-first: %+v", got.Reviewer)
	}
	if src := prov.Source("reviewer", "model"); src != SourceProfile("strong") {
		t.Errorf("model source = %q, want %q", src, SourceProfile("strong"))
	}
	if src := prov.Source("reviewer", "tools"); src != SourceProfile("base") {
		t.Errorf("tools source = %q, want %q", src, SourceProfile("base"))
	}
}

// TestOneProfileEditPropagatesToConsumersOnly is AC5, proven across a multi-repo
// fixture: repo A consumes the profile, repo B consumes it but pins one field,
// and repo C is fully inline. Editing the profile changes A entirely, changes B
// except where it is pinned, and leaves C byte-identical.
func TestOneProfileEditPropagatesToConsumersOnly(t *testing.T) {
	before := `
[profiles.shared]
role    = "reviewer"
command = "claude -p {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "sonnet"
`
	after := `
[profiles.shared]
role    = "reviewer"
command = "grok -p {payload} --system-prompt-override {system}"
tools   = "read_file,grep,list_dir"
model   = "grok-4.5"
`
	repoA := repoAgents(t, "[reviewer]\nprofile = \"shared\"\n")
	repoB := repoAgents(t, "[reviewer]\nprofile = \"shared\"\nmodel = \"pinned-model\"\n")
	repoC := repoAgents(t, "[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\nmodel = \"inline-model\"\n")

	resolve := func(repo AgentsConfig, body string) AgentBinding {
		t.Helper()
		got, _, err := ResolveAgents(repo, catalog(t, body))
		if err != nil {
			t.Fatalf("ResolveAgents: %v", err)
		}
		return got.Reviewer
	}
	a0, b0, c0 := resolve(repoA, before), resolve(repoB, before), resolve(repoC, before)
	a1, b1, c1 := resolve(repoA, after), resolve(repoB, after), resolve(repoC, after)

	if a0.Command == a1.Command || a1.Model != "grok-4.5" {
		t.Errorf("repo A consumes the profile and must track the edit: %+v -> %+v", a0, a1)
	}
	if b0.Command == b1.Command {
		t.Errorf("repo B must track the profile's unpinned fields: %+v -> %+v", b0, b1)
	}
	if b0.Model != "pinned-model" || b1.Model != "pinned-model" {
		t.Errorf("repo B pinned model must survive the edit: %q -> %q", b0.Model, b1.Model)
	}
	if !reflect.DeepEqual(c0, c1) {
		t.Errorf("repo C is inline and must be untouched: %+v -> %+v", c0, c1)
	}
}

// TestLayerVarsRepoWinsPerKey pins the ${VAR} layering direction: the catalog is
// the machine-wide base and the repo (including its gitignored local overlay)
// wins per key — never the other way round.
func TestLayerVarsRepoWinsPerKey(t *testing.T) {
	got := LayerVars(map[string]string{"SHARED": "global", "ONLY_GLOBAL": "g"},
		map[string]string{"SHARED": "repo", "ONLY_REPO": "r"})
	want := map[string]string{"SHARED": "repo", "ONLY_GLOBAL": "g", "ONLY_REPO": "r"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LayerVars = %+v, want %+v", got, want)
	}
	if LayerVars(nil, nil) != nil {
		t.Error("no vars anywhere must stay nil (zero-config repos unchanged)")
	}
}

// TestCatalogSecretNeverEntersTheRepo pins AC6's containment rule: a secret
// defined in the machine-wide [vars] resolves for a profile-consuming repo at
// wiring time, in memory — while nothing under the repo's data dir ever gains
// the literal value.
func TestCatalogSecretNeverEntersTheRepo(t *testing.T) {
	testutil.IsolateHome(t)
	const secret = "sk-do-not-copy-me"
	writeGlobalCatalog(t, `
[vars]
TOKEN = "`+secret+`"

[profiles.remote]
role    = "reviewer"
command = "claude -p {system}"
tools   = "Read,Grep,Glob"
env     = { ANTHROPIC_AUTH_TOKEN = "${TOKEN}" }
`)
	dataDir := t.TempDir()
	repoBody := "[reviewer]\nprofile = \"remote\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, AgentsConfigName), []byte(repoBody), 0o644); err != nil {
		t.Fatal(err)
	}
	eff, err := LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveAgents: %v", err)
	}
	resolved, err := ResolveAgentEnvs(eff.Agents, eff.Vars)
	if err != nil {
		t.Fatalf("ResolveAgentEnvs: %v", err)
	}
	if resolved.Reviewer.Env["ANTHROPIC_AUTH_TOKEN"] != secret {
		t.Errorf("the catalog var must expand in memory, got %q", resolved.Reviewer.Env["ANTHROPIC_AUTH_TOKEN"])
	}
	// Nothing under the repo data dir may carry the literal.
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(dataDir, e.Name()))
		if rerr != nil {
			continue
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret material leaked into the repo file %s", e.Name())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dataDir, AgentsConfigName)); string(got) != repoBody {
		t.Errorf("resolution must not rewrite the repo file: %q", string(got))
	}
}

// TestCatalogCannotSupplyWorkflowSubstrate is AC7's structural guard: a global
// home stuffed with workflows/, skills/, and principles/ resolves exactly like
// one holding only the catalog. The catalog loader reads three sections and
// nothing else, so there is no path from the machine-wide home into a repo's
// process substrate.
func TestCatalogCannotSupplyWorkflowSubstrate(t *testing.T) {
	testutil.IsolateHome(t)
	body := "[profiles.p]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	writeGlobalCatalog(t, body)
	clean, err := LoadGlobalAgents()
	if err != nil {
		t.Fatalf("LoadGlobalAgents: %v", err)
	}
	for _, kind := range []string{"workflows", "skills", "principles", "tasks", "documents"} {
		dir := filepath.Join(GlobalDir(), kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hijack.md"), []byte("---\nname: hijack\napplies_to: [\"*\"]\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stuffed, err := LoadGlobalAgents()
	if err != nil {
		t.Fatalf("LoadGlobalAgents (stuffed home): %v", err)
	}
	if !reflect.DeepEqual(clean, stuffed) {
		t.Errorf("substrate directories in the global home must be invisible to the catalog:\n %+v\n %+v", clean, stuffed)
	}
	repo := repoAgents(t, "[reviewer]\nprofile = \"p\"\n")
	a, _, err := ResolveAgents(repo, clean)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	b, _, err := ResolveAgents(repo, stuffed)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("resolution must not vary with the global home's substrate dirs")
	}
}

// TestProfileSelectionIsConfigurationNotCode is AC7's configuration-over-code
// half: which profile a repo runs is decided entirely by the two TOML files. The
// same unchanged Go path yields a different effective binding when only the
// fixture text changes — there is no compiled branch choosing a profile.
func TestProfileSelectionIsConfigurationNotCode(t *testing.T) {
	cat := catalog(t, twoProfileCatalog)
	command, _, err := ResolveAgents(repoAgents(t, "[reviewer]\nprofile = \"claude-opus\"\n"), cat)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	acp, _, err := ResolveAgents(repoAgents(t, "[reviewer]\nprofile = \"grok-acp\"\n"), cat)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	if command.Reviewer.ResolvedInterface() != InterfaceCommand || acp.Reviewer.ResolvedInterface() != InterfaceACP {
		t.Errorf("the referenced profile alone must decide the transport: %q vs %q",
			command.Reviewer.ResolvedInterface(), acp.Reviewer.ResolvedInterface())
	}
	if command.Reviewer.Model == acp.Reviewer.Model {
		t.Error("a fixture-only change must flip the effective binding")
	}
}
