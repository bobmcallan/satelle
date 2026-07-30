package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// TestGlobalAgentsRoundTripsEveryExecutionField pins AC1: a profile carries the
// full provider-neutral EXECUTION surface — role, interface, command, model,
// effort, tools, timeout, env references, settings, principles, and secondary —
// and every one of them survives the load.
func TestGlobalAgentsRoundTripsEveryExecutionField(t *testing.T) {
	body := `
[profiles.claude-opus]
role       = "reviewer"
interface  = "command"
command    = "claude -p --append-system-prompt {system} --allowedTools {tools} --model {model}"
tools      = "Read,Grep,Glob"
model      = "opus"
effort     = "high"
timeout    = "45m"
principles = "session"
secondary  = "grok-fallback"
env        = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }
settings   = { model = "opus" }

[profiles.grok-fallback]
role      = "reviewer"
interface = "acp"
command   = "grok agent stdio"
tools     = "read_file,grep,list_dir"
model     = "grok-4.5"
`
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatalf("ParseGlobalAgents: %v", err)
	}
	p, ok := g.Profiles["claude-opus"]
	if !ok {
		t.Fatal("claude-opus profile not loaded")
	}
	checks := []struct{ field, got, want string }{
		{"role", p.Role, RoleReviewer},
		{"interface", p.ResolvedInterface(), InterfaceCommand},
		{"tools", p.Tools, "Read,Grep,Glob"},
		{"model", p.Model, "opus"},
		{"effort", p.Effort, "high"},
		{"timeout", p.Timeout, "45m"},
		{"principles", p.ResolvedPrinciples(), PrinciplesSession},
		{"secondary", p.Secondary, "grok-fallback"},
		{"env", p.Env["ANTHROPIC_AUTH_TOKEN"], "${GLM_API_KEY}"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if !strings.Contains(p.Command, "{system}") {
		t.Errorf("command not round-tripped: %q", p.Command)
	}
	if p.Settings["model"] != "opus" {
		t.Errorf("settings not round-tripped: %+v", p.Settings)
	}
	if g.Profiles["grok-fallback"].ResolvedInterface() != InterfaceACP {
		t.Errorf("second profile interface = %q, want acp", g.Profiles["grok-fallback"].ResolvedInterface())
	}
}

// TestGlobalAgentsRefusesWorkflowPolicy pins AC1 and AC7: the catalog is
// EXECUTION configuration only. A profile that tries to decide process — naming
// a skill, a gate, an applies_to, an output contract — is refused at load with
// an error that teaches the boundary, so policy can never leak machine-wide.
func TestGlobalAgentsRefusesWorkflowPolicy(t *testing.T) {
	for _, key := range []string{
		`applies_to = ["*"]`,
		`skill = "satelle-story-plan-review"`,
		`review_skill = "satelle-code-ac-review"`,
		`workflow = "satelle-project-workflow"`,
		`create_review = "satelle-story-create-review"`,
		`prompt = "@skill:plan"`,
		`on = "integration"`,
		`output_name = "plan"`,
		`on_enter_agent = "retrospective"`,
	} {
		body := "[profiles.p]\nrole = \"agent\"\n" + key + "\n"
		_, err := ParseGlobalAgents(body)
		if err == nil {
			t.Errorf("%s: want a refusal — a profile must not carry workflow policy", key)
			continue
		}
		if !strings.Contains(err.Error(), "policy") && !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("%s: error should name the boundary: %v", key, err)
		}
	}
}

// TestGlobalAgentsRefusesUnknownSectionAndKey pins the mechanical guard: only
// [vars]/[profiles.*]/[roles] and the known binding keys exist. A stray section
// (a machine-wide [workflows] attempt) or a mistyped key fails loud rather than
// being silently dropped.
func TestGlobalAgentsRefusesUnknownSectionAndKey(t *testing.T) {
	if _, err := ParseGlobalAgents("[workflows]\nname = \"x\"\n"); err == nil {
		t.Error("want a refusal for a top-level [workflows] section")
	} else if !strings.Contains(err.Error(), "repo substrate") {
		t.Errorf("error should say workflows stay repo substrate: %v", err)
	}
	if _, err := ParseGlobalAgents("[profiles.p]\nmodle = \"opus\"\n"); err == nil {
		t.Error("want a refusal for a mistyped profile key")
	}
	// The retired aliases are not resurrected in a brand-new file format.
	if _, err := ParseGlobalAgents("[profiles.p]\nharness = \"claude\"\n"); err == nil {
		t.Error("want a refusal for the retired harness= alias")
	}
}

// TestGlobalAgentsRefusesInvalidBindingValues pins AC4's refusal set at the
// catalog layer: an invalid interface, an unparseable timeout, and a role that
// is neither reviewer nor agent are all caught at load.
func TestGlobalAgentsRefusesInvalidBindingValues(t *testing.T) {
	cases := map[string]string{
		"invalid interface": "[profiles.p]\ninterface = \"telepathy\"\n",
		"bad timeout":       "[profiles.p]\ntimeout = \"soon\"\n",
		"unknown role":      "[profiles.p]\nrole = \"overlord\"\n",
	}
	for name, body := range cases {
		if _, err := ParseGlobalAgents(body); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

// TestGlobalAgentsRefusesBrokenReferences pins AC4: a missing profile, a
// reference cycle, and a [roles] default naming no profile are all hard errors
// naming the offender — resolution never half-applies a broken catalog.
func TestGlobalAgentsRefusesBrokenReferences(t *testing.T) {
	_, err := ParseGlobalAgents("[profiles.p]\nprofile = \"nope\"\n")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("missing profile reference must be refused by name: %v", err)
	}
	_, err = ParseGlobalAgents("[profiles.a]\nprofile = \"b\"\n[profiles.b]\nprofile = \"a\"\n")
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("reference cycle must be refused: %v", err)
	}
	_, err = ParseGlobalAgents("[profiles.a]\nrole = \"reviewer\"\n[roles]\nreviewer = \"missing\"\n")
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("[roles] default naming no profile must be refused: %v", err)
	}
	_, err = ParseGlobalAgents("[profiles.a]\nrole = \"reviewer\"\n[roles]\noverlord = \"a\"\n")
	if err == nil {
		t.Error("[roles] key must be a known role")
	}
}

// TestGlobalAgentsChainResolves pins the one legitimate reference use: a profile
// extending another, with the outermost winning field by field.
func TestGlobalAgentsChainResolves(t *testing.T) {
	g, err := ParseGlobalAgents(`
[profiles.base]
role    = "reviewer"
command = "claude -p {system}"
tools   = "Read,Grep,Glob"
model   = "sonnet"

[profiles.strong]
profile = "base"
model   = "opus"
`)
	if err != nil {
		t.Fatalf("ParseGlobalAgents: %v", err)
	}
	chain, err := g.resolveChain("strong")
	if err != nil {
		t.Fatalf("resolveChain: %v", err)
	}
	if len(chain) != 2 || chain[0] != "strong" || chain[1] != "base" {
		t.Errorf("chain = %v, want [strong base]", chain)
	}
}

// TestLoadGlobalAgentsAbsentIsZero pins the zero-config guarantee: a machine
// with no catalog loads a clean zero value and a nil error, so nothing about
// existing installations changes until an operator creates the file.
func TestLoadGlobalAgentsAbsentIsZero(t *testing.T) {
	testutil.IsolateHome(t)
	g, err := LoadGlobalAgents()
	if err != nil {
		t.Fatalf("absent catalog must not error: %v", err)
	}
	if len(g.Profiles) != 0 || len(g.Roles) != 0 || len(g.Vars) != 0 {
		t.Errorf("absent catalog must be the zero value: %+v", g)
	}
}

// TestLoadGlobalAgentsReadsTheHome pins the location: $SATELLE_HOME/agents.toml,
// sibling of config.toml.
func TestLoadGlobalAgentsReadsTheHome(t *testing.T) {
	home := testutil.IsolateHome(t)
	writeGlobalCatalog(t, "[profiles.p]\nrole = \"agent\"\nmodel = \"opus\"\n")
	if want := filepath.Join(home, GlobalAgentsName); GlobalAgentsPath() != want {
		t.Errorf("GlobalAgentsPath = %q, want %q", GlobalAgentsPath(), want)
	}
	g, err := LoadGlobalAgents()
	if err != nil {
		t.Fatalf("LoadGlobalAgents: %v", err)
	}
	if g.Profiles["p"].Model != "opus" {
		t.Errorf("catalog not read from the home: %+v", g.Profiles)
	}
}

// TestStarterGlobalAgentsIsSecretFreeAndValid pins AC6's migration content: the
// starter catalog derived from the machine's [agent] cli parses under the
// catalog's own rules, carries a real command template, and embeds no secret —
// only a commented ${VAR} reference.
func TestStarterGlobalAgentsIsSecretFreeAndValid(t *testing.T) {
	body, err := StarterGlobalAgents("claude")
	if err != nil {
		t.Fatalf("StarterGlobalAgents: %v", err)
	}
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatalf("the starter catalog must satisfy its own loader: %v", err)
	}
	p, ok := g.Profiles["claude-reviewer"]
	if !ok {
		t.Fatalf("starter catalog defines no claude-reviewer: %+v", g.Profiles)
	}
	if !strings.Contains(p.Command, "{system}") || p.Role != RoleReviewer {
		t.Errorf("starter profile is not a usable reviewer binding: %+v", p)
	}
	// Role defaults stay commented out: migration must not change how any repo
	// resolves until the operator opts in.
	if len(g.Roles) != 0 {
		t.Errorf("starter catalog must not enable role defaults: %+v", g.Roles)
	}
	if _, err := StarterGlobalAgents("not-a-cli"); err == nil {
		t.Error("an unknown CLI must not produce a catalog")
	}
}

// writeGlobalCatalog writes body to the isolated home's catalog path.
func writeGlobalCatalog(t testing.TB, body string) {
	t.Helper()
	if err := os.MkdirAll(GlobalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalAgentsPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
