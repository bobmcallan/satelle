package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cloudCatalog = `
[vars]
OPS_AZ_SECRET = "az-secret-value"
OPS_AWS_KEY = "AKIAEXAMPLE"
OPS_AWS_SECRET = "aws-secret-value"

[profiles.ops]
role = "agent"

[profiles.ops.cloud.azure]
tenant = "t-1"
client_id = "c-1"
client_secret = "${OPS_AZ_SECRET}"
subscription = "s-1"

[profiles.ops.cloud.aws]
access_key_id = "${OPS_AWS_KEY}"
secret_access_key = "${OPS_AWS_SECRET}"
region = "ap-southeast-2"
role_arn = "arn:aws:iam::1:role/ops"
`

func mustCatalog(t *testing.T, body string) GlobalAgentsConfig {
	t.Helper()
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatalf("ParseGlobalAgents: %v", err)
	}
	return g
}

// AC1: a valid azure + aws identity parses; each malformed shape is refused with
// the field named, and a literal secret is refused without echoing its value.
func TestParseGlobalAgents_CloudIdentity(t *testing.T) {
	g := mustCatalog(t, cloudCatalog)
	if got := g.Profiles["ops"].Cloud["azure"]["client_secret"]; got != "${OPS_AZ_SECRET}" {
		t.Errorf("azure client_secret = %q, want the unexpanded reference", got)
	}
	if g.Profiles["ops"].Cloud["aws"]["region"] != "ap-southeast-2" {
		t.Errorf("aws region not parsed: %v", g.Profiles["ops"].Cloud["aws"])
	}

	cases := []struct {
		name, body, want string
	}{
		{"unknown provider", "[profiles.ops.cloud.gcp]\nproject = \"p\"\n", "cloud.gcp: unknown provider"},
		{"unknown key", "[profiles.ops.cloud.azure]\ntenant=\"t\"\nclient_id=\"c\"\nclient_secret=\"${S}\"\nbogus=\"x\"\n", "unknown key(s): bogus"},
		{"missing tenant", "[profiles.ops.cloud.azure]\nclient_id=\"c\"\nclient_secret=\"${S}\"\n", "tenant is required"},
		{"literal azure secret", "[profiles.ops.cloud.azure]\ntenant=\"t\"\nclient_id=\"c\"\nclient_secret=\"abc-literal\"\n", "cloud.azure.client_secret: literal secret"},
		{"literal aws key", "[profiles.ops.cloud.aws]\naccess_key_id=\"AKIALITERAL\"\nsecret_access_key=\"${S}\"\n", "cloud.aws.access_key_id: literal secret"},
		{"embedded reference", "[profiles.ops.cloud.aws]\naccess_key_id=\"pre-${K}\"\nsecret_access_key=\"${S}\"\n", "cloud.aws.access_key_id: literal secret"},
		{"newline", "[profiles.ops.cloud.aws]\naccess_key_id=\"${K}\"\nsecret_access_key=\"${S}\"\nregion=\"a\\nb\"\n", "cloud.aws.region: must be a single line"},
		{"bad profile name", "[profiles.\"a b\".cloud.aws]\naccess_key_id=\"${K}\"\nsecret_access_key=\"${S}\"\n", "cannot carry a cloud identity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseGlobalAgents(c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			for _, secret := range []string{"abc-literal", "AKIALITERAL"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error echoes the literal secret %q: %v", secret, err)
				}
			}
		})
	}
}

// AC1: a repo binding cannot carry credentials; it selects an identity only by
// naming a catalog profile.
func TestLoadAgents_RepoBindingCloudRefused(t *testing.T) {
	_, err := loadAgentsBody("[executor]\ncommand = \"in-loop\"\n[executor.cloud.aws]\naccess_key_id = \"${K}\"\n")
	if err == nil || !strings.Contains(err.Error(), "a repo binding cannot carry a cloud identity") {
		t.Fatalf("err = %v, want the repo-binding cloud refusal", err)
	}
}

func effectiveFor(t *testing.T, g GlobalAgentsConfig, repoAgents string) EffectiveAgents {
	t.Helper()
	repo, err := loadAgentsBody(repoAgents)
	if err != nil {
		t.Fatalf("loadAgentsBody: %v", err)
	}
	eff, err := ResolveEffectiveAgents(repo, g, nil)
	if err != nil {
		t.Fatalf("ResolveEffectiveAgents: %v", err)
	}
	return eff
}

// profile= on the executor seat keeps it in-loop, and the resolver reads the
// profile's cloud table (not its command).
func TestInLoopCloudIdentity_SelectsForInLoopSeatWithProfile(t *testing.T) {
	g := mustCatalog(t, cloudCatalog)
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n")
	if !IsInLoopCommand(eff.Agents.ExecutorBinding().Command) {
		t.Fatalf("executor with profile= is no longer in-loop: %q", eff.Agents.ExecutorBinding().Command)
	}
	sel, ok, err := InLoopCloudIdentity(eff, g, nil)
	if err != nil || !ok {
		t.Fatalf("selection = (%+v, %v, %v), want an identity", sel, ok, err)
	}
	if sel.Profile != "ops" {
		t.Errorf("profile = %q, want ops", sel.Profile)
	}
	if got := sel.ProviderNames(); len(got) != 2 || got[0] != "aws" || got[1] != "azure" {
		t.Errorf("providers = %v, want [aws azure]", got)
	}
	if sel.Providers["azure"]["client_secret"] != "az-secret-value" {
		t.Errorf("azure secret not expanded from [vars]: %v", sel.Providers["azure"])
	}
	if sel.Providers["aws"]["secret_access_key"] != "aws-secret-value" {
		t.Errorf("aws secret not expanded from [vars]: %v", sel.Providers["aws"])
	}
}

func TestInLoopCloudIdentity_NoSelection(t *testing.T) {
	g := mustCatalog(t, cloudCatalog+"\n[profiles.plain]\nrole = \"agent\"\n")
	cases := []struct{ name, agents string }{
		{"no profile", "[executor]\ncommand = \"in-loop\"\n"},
		{"profile without cloud", "[executor]\ncommand = \"in-loop\"\nprofile = \"plain\"\n"},
		{"seat not in-loop", "[executor]\ncommand = \"claude -p {system}\"\nprofile = \"ops\"\n"},
		{"empty repo agents", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := effectiveFor(t, g, c.agents)
			if sel, ok, err := InLoopCloudIdentity(eff, g, nil); ok || err != nil {
				t.Fatalf("selection = (%+v, %v, %v), want none", sel, ok, err)
			}
		})
	}
}

// A profile that extends another inherits its cloud identity.
func TestInLoopCloudIdentity_ExtendedProfile(t *testing.T) {
	g := mustCatalog(t, cloudCatalog+"\n[profiles.child]\nprofile = \"ops\"\n")
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"child\"\n")
	sel, ok, err := InLoopCloudIdentity(eff, g, nil)
	if err != nil || !ok || sel.Profile != "child" {
		t.Fatalf("selection = (%+v, %v, %v), want the inherited identity under child", sel, ok, err)
	}
}

// Credentials resolve from the machine [vars] and the gitignored local overlay
// only. A name defined solely in the committed repo [vars] is not a source.
func TestInLoopCloudIdentity_VarsSources(t *testing.T) {
	body := strings.Replace(cloudCatalog, `OPS_AZ_SECRET = "az-secret-value"`, "", 1)
	g := mustCatalog(t, body)
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n")

	_, ok, err := InLoopCloudIdentity(eff, g, nil)
	if !ok || err == nil || !strings.Contains(err.Error(), "${OPS_AZ_SECRET}") {
		t.Fatalf("missing reference: ok=%v err=%v, want ok with an error naming ${OPS_AZ_SECRET}", ok, err)
	}
	// The committed repo [vars] reaches eff.Vars but must not reach a credential.
	eff.Vars = LayerVars(g.Vars, map[string]string{"OPS_AZ_SECRET": "from-committed"})
	if _, _, err := InLoopCloudIdentity(eff, g, nil); err == nil {
		t.Fatal("a reference defined only in the committed repo [vars] resolved")
	}
	// The gitignored local overlay is a source.
	sel, ok, err := InLoopCloudIdentity(eff, g, map[string]string{"OPS_AZ_SECRET": "from-local"})
	if err != nil || !ok || sel.Providers["azure"]["client_secret"] != "from-local" {
		t.Fatalf("local overlay: (%+v, %v, %v), want the local value", sel, ok, err)
	}
}

func TestCloudRefProblems(t *testing.T) {
	body := strings.Replace(cloudCatalog, `OPS_AZ_SECRET = "az-secret-value"`, "", 1)
	body = strings.Replace(body, `OPS_AWS_KEY = "AKIAEXAMPLE"`, "", 1)
	g := mustCatalog(t, body)
	got := strings.Join(CloudRefProblems(g, nil, map[string]string{"OPS_AWS_KEY": "committed"}), "\n")
	if !strings.Contains(got, "${OPS_AZ_SECRET} is not defined") {
		t.Errorf("absent reference not reported:\n%s", got)
	}
	if !strings.Contains(got, "${OPS_AWS_KEY} is defined only in the committed repo [vars]") {
		t.Errorf("committed-only reference not reported:\n%s", got)
	}
	if strings.Contains(got, "OPS_AWS_SECRET") {
		t.Errorf("a defined reference was reported:\n%s", got)
	}
	if p := CloudRefProblems(g, map[string]string{"OPS_AZ_SECRET": "x", "OPS_AWS_KEY": "y"}, nil); len(p) != 0 {
		t.Errorf("local overlay should satisfy the references, got %v", p)
	}
}

func TestLocalVarsAt_ReadsOnlyTheLocalOverlay(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, DefaultDataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ConfigName), []byte("[vars]\nCOMMITTED = \"c\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, LocalConfigName), []byte("[vars]\nLOCAL = \"l\"\n"), 0o644)
	got := LocalVarsAt(root)
	if got["LOCAL"] != "l" || len(got) != 1 {
		t.Errorf("LocalVarsAt = %v, want only the local overlay", got)
	}
	if LocalVarsAt(t.TempDir()) != nil {
		t.Error("an absent overlay should be nil")
	}
}

func TestOverlayBindingMergesCloudProviderWise(t *testing.T) {
	base := AgentBinding{Cloud: map[string]map[string]string{"azure": {"tenant": "base"}, "aws": {"region": "base"}}}
	top := AgentBinding{Cloud: map[string]map[string]string{"aws": {"region": "top"}}}
	out, src := overlayBinding(base, map[string]string{"cloud": "B"}, top, "T")
	if out.Cloud["azure"]["tenant"] != "base" || out.Cloud["aws"]["region"] != "top" {
		t.Errorf("cloud = %v, want azure kept and aws replaced", out.Cloud)
	}
	if src["cloud"] != "T+B" {
		t.Errorf("cloud source = %q, want T+B", src["cloud"])
	}
	if base.Cloud["aws"]["region"] != "base" {
		t.Error("overlay mutated the base binding")
	}
}

// AC6: the starter catalog says satelle selects and does not enforce, and names
// what each provider persists.
func TestStarterGlobalAgents_CloudIdentityText(t *testing.T) {
	s, err := StarterGlobalAgents("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"does not enforce rights", "roles and policy", "separate OS user",
		"Azure: the az CLI stores the service-principal secret in its cache under AZURE_CONFIG_DIR (~/.satelle/cloud/<profile>/azure)",
		"AWS: the agent receives only STS session credentials via credential_process",
	} {
		if !strings.Contains(strings.ReplaceAll(s, "\n# ", " "), want) {
			t.Errorf("starter catalog lacks %q", want)
		}
	}
	// The starter must still parse: the commented identity changes nothing.
	if _, err := ParseGlobalAgents(s); err != nil {
		t.Errorf("starter catalog does not parse: %v", err)
	}
}
