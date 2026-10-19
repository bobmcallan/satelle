package config

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// oldRedactAgents is the transport redactor this story retired, FROZEN here as a
// fixture: each checker row below proves the input it refuses is content the old
// push rewrote silently, so the checker covers everything the redactor touched.
func oldRedactAgents(a AgentsConfig) AgentsConfig {
	out := AgentsConfig{Defaults: a.Defaults, ModelOrder: a.ModelOrder, Executor: oldRedactBinding(a.Executor), Reviewer: oldRedactBinding(a.Reviewer)}
	if len(a.Agents) > 0 {
		out.Agents = make(map[string]AgentBinding, len(a.Agents))
		for name, b := range a.Agents {
			out.Agents[name] = oldRedactBinding(b)
		}
	}
	return out
}

func oldRedactValue(v string) string {
	if IsVarRef(v) {
		return strings.TrimSpace(v)
	}
	return ""
}

func oldRedactBinding(b AgentBinding) AgentBinding {
	out := b
	fields := strings.Fields(b.CommandTemplate())
	for i, f := range fields {
		if filepath.IsAbs(f) {
			fields[i] = filepath.Base(f)
		}
	}
	out.Command = strings.Join(fields, " ")
	out.Harness = ""
	out.Profile = ""
	if len(b.Env) > 0 {
		out.Env = make(map[string]string, len(b.Env))
		for k, v := range b.Env {
			out.Env[k] = oldRedactValue(v)
		}
	}
	if len(b.Settings) > 0 {
		out.Settings = oldRedactSettings(b.Settings, false)
	}
	return out
}

func oldRedactSettings(m map[string]any, underEnv bool) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch child := v.(type) {
		case map[string]any:
			out[k] = oldRedactSettings(child, underEnv || strings.ToLower(k) == "env")
		case []any:
			out[k] = oldRedactList(child)
		default:
			if underEnv || SecretishKey(k) {
				if s, ok := v.(string); ok {
					out[k] = oldRedactValue(s)
				} else {
					out[k] = ""
				}
				continue
			}
			out[k] = oldRedactScalar(v)
		}
	}
	return out
}

func oldRedactList(l []any) []any {
	out := make([]any, len(l))
	for i, v := range l {
		switch child := v.(type) {
		case map[string]any:
			out[i] = oldRedactSettings(child, false)
		case []any:
			out[i] = oldRedactList(child)
		default:
			out[i] = oldRedactScalar(v)
		}
	}
	return out
}

func oldRedactScalar(v any) any {
	if s, ok := v.(string); ok && filepath.IsAbs(s) {
		return filepath.Base(s)
	}
	return v
}

// oldRedactorAlters reports whether the frozen redactor changed the layer a body
// decodes to — compared through the encoder, so ordering and nil-vs-empty maps
// cannot make an unchanged layer look altered.
func oldRedactorAlters(t *testing.T, body string) bool {
	t.Helper()
	ac, err := decodeAgents(body, false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	before, err := EncodeAgents(ac)
	if err != nil {
		t.Fatal(err)
	}
	after, err := EncodeAgents(oldRedactAgents(ac))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		return true
	}
	// harness= is retired and the encoder never writes it, so compare it directly.
	redacted := oldRedactAgents(ac)
	if ac.Executor.Harness != redacted.Executor.Harness || ac.Reviewer.Harness != redacted.Reviewer.Harness {
		return true
	}
	for name, b := range ac.Agents {
		if b.Harness != redacted.Agents[name].Harness {
			return true
		}
	}
	return false
}

// TestCheckAgentsRules is the checker table (sty_89b9df20 AC1): one row per
// machine-specific rule. Every row but (f) also proves the retired redactor
// rewrote that input; (f) was never the redactor's — it is the refusal only.
func TestCheckAgentsRules(t *testing.T) {
	rows := []struct {
		name         string
		body         string
		rule         string
		section      string
		field        string
		oldRedactAlt bool
	}{
		{"a settings env literal", "[reviewer]\nsettings = { env = { ANTHROPIC_BASE_URL = \"https://api.example.test\" } }\n",
			RuleSettingsEnv, "reviewer", "settings.env.ANTHROPIC_BASE_URL", true},
		{"b settings secret literal", "[reviewer]\nsettings = { apiKey = \"sk-live-123\" }\n",
			RuleSettingsSecret, "reviewer", "settings.apiKey", true},
		{"c settings path in a list", "[reviewer]\nsettings = { permissions = { additionalDirectories = [\"/home/someone/proj\", \"relative/dir\"] } }\n",
			RuleSettingsPath, "reviewer", "settings.permissions.additionalDirectories[0]", true},
		{"c settings path in a nested table", "[coder]\nsettings = { sandbox = { root = \"/srv/work\" } }\n",
			RuleSettingsPath, "coder", "settings.sandbox.root", true},
		{"c settings path in a list of tables", "[coder]\nsettings = { mounts = [ { source = \"/srv/work\" } ] }\n",
			RuleSettingsPath, "coder", "settings.mounts[0].source", true},
		{"d absolute command token", "[reviewer]\ncommand = \"/opt/local/bin/claude -p --model {model}\"\n",
			RuleCommandPath, "reviewer", "command", true},
		{"e profile", "[reviewer]\nprofile = \"claude-opus\"\n",
			RuleProfile, "reviewer", "profile", true},
		{"e harness", "[coder]\nharness = \"claude\"\n",
			RuleHarness, "coder", "harness", true},
		{"f use_global_roles", "[defaults]\nuse_global_roles = true\n",
			RuleGlobalRoles, "defaults", "use_global_roles", false},
		{"g executor env literal", "[executor]\ncommand = \"in-loop\"\nenv = { GOBIN = \"/tmp/bin\" }\n",
			RuleBindingEnv, "executor", "env.GOBIN", true},
		{"g reviewer env literal", "[reviewer]\nenv = { ANTHROPIC_BASE_URL = \"https://api.example.test\" }\n",
			RuleBindingEnv, "reviewer", "env.ANTHROPIC_BASE_URL", true},
		{"g named binding env literal", "[coder]\nenv = { ANTHROPIC_AUTH_TOKEN = \"sk-live\" }\n",
			RuleBindingEnv, "coder", "env.ANTHROPIC_AUTH_TOKEN", true},
		{"g composite is literal", "[coder]\nenv = { GOBIN = \"${SATELLE_SCRATCH}/bin\" }\n",
			RuleBindingEnv, "coder", "env.GOBIN", true},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got, err := CheckAgentsTransport([]byte(r.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("want exactly one refusal, got %d: %v", len(got), got)
			}
			x := got[0]
			if x.Rule != r.rule || x.Section != r.section || x.Field != r.field || x.File != AgentsConfigName {
				t.Errorf("refusal = %+v, want rule %q section %q field %q", x, r.rule, r.section, r.field)
			}
			msg := x.Error()
			for _, want := range []string{AgentsConfigName, "[" + r.section + "]", r.field, ".satelle/workflows/agents.local.toml", "never leaves this machine"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal text %q lacks %q", msg, want)
				}
			}
			if r.oldRedactAlt != oldRedactorAlters(t, r.body) {
				t.Errorf("frozen redactor altered = %v, want %v", !r.oldRedactAlt, r.oldRedactAlt)
			}
		})
	}
}

// TestCheckAgentsAllowsShareableContent: nothing a machine could not share is
// refused — pure ${VAR} references, blank values (declared, unsatisfied),
// relative paths, placeholders, and non-secret settings.
func TestCheckAgentsAllowsShareableContent(t *testing.T) {
	body := `
[defaults]
secondary = "reviewer"

[executor]
role = "agent"
command = "in-loop"

[reviewer]
role = "reviewer"
command = "claude -p --output-format json --append-system-prompt {system} --model {model}"
tools = "Read,Grep,Glob"
model = "opus"
env = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}", BLANK = "" }
settings = { model = "opus", apiKey = "${CLAUDE_API_KEY}", env = { TOKEN = "${T}", EMPTY = "" }, permissions = { allow = ["Read"], additionalDirectories = ["relative/dir"] } }

[coder]
role = "agent"
interface = "stream"
command = "claude -p --input-format stream-json --allowedTools {tools}"
`
	got, err := CheckAgentsTransport([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("shareable content refused: %v", got)
	}
}

// TestCheckAgentsReportsEverythingInOrder: a layer with several defects is
// reported whole, in a fixed order, so a push names every move in one pass.
func TestCheckAgentsReportsEverythingInOrder(t *testing.T) {
	body := `
[defaults]
use_global_roles = true

[reviewer]
profile = "claude-opus"
command = "/opt/bin/claude -p"

[coder]
env = { A = "literal" }
`
	got, err := CheckAgentsTransport([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, r := range got {
		fields = append(fields, r.Section+"."+r.Field)
	}
	want := []string{"defaults.use_global_roles", "reviewer.profile", "reviewer.command", "coder.env.A"}
	if strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("refusals = %v, want %v", fields, want)
	}
	err = RefusalsError("sync config push", got)
	if err == nil || !strings.HasPrefix(err.Error(), "sync config push refused") {
		t.Fatalf("RefusalsError = %v", err)
	}
	for _, r := range got {
		if !strings.Contains(err.Error(), r.Error()) {
			t.Errorf("combined error lacks %q:\n%v", r.Error(), err)
		}
	}
	if RefusalsError("x", nil) != nil {
		t.Error("no refusals must be no error")
	}
}

func TestCheckAgentsTransportRefusesUnparseable(t *testing.T) {
	if _, err := CheckAgentsTransport([]byte("[reviewer\ncommand = ")); err == nil {
		t.Fatal("an unparseable agents body must be an error, never judged clean")
	}
}

func TestIsVarRef(t *testing.T) {
	for v, want := range map[string]bool{"${TOKEN}": true, " ${a_1} ": true, "": false, "literal": false, "Bearer ${TOKEN}": false, "${}": false, "${1x}": false} {
		if got := IsVarRef(v); got != want {
			t.Errorf("IsVarRef(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestPruneUnsatisfied: a blank a workspace author left is "declared,
// unsatisfied" — dropped from the binding — while references and real values stay.
func TestPruneUnsatisfied(t *testing.T) {
	b := AgentBinding{
		Env:      map[string]string{"BLANK": "", "REF": "${X}"},
		Settings: map[string]any{"model": "opus", "apiKey": "", "env": map[string]any{"A": "", "B": "${B}"}, "permissions": map[string]any{"allow": []any{"Read"}}},
	}
	got := PruneUnsatisfied(b)
	if _, ok := got.Env["BLANK"]; ok || got.Env["REF"] != "${X}" {
		t.Errorf("env = %+v", got.Env)
	}
	if _, ok := got.Settings["apiKey"]; ok {
		t.Errorf("blank secret setting must be dropped: %+v", got.Settings)
	}
	envTbl, _ := got.Settings["env"].(map[string]any)
	if _, ok := envTbl["A"]; ok || envTbl["B"] != "${B}" {
		t.Errorf("settings.env = %+v", envTbl)
	}
	if got.Settings["model"] != "opus" {
		t.Errorf("real settings must stay: %+v", got.Settings)
	}
	if PruneUnsatisfied(AgentBinding{Env: map[string]string{"ONLY": ""}}).Env != nil {
		t.Error("an env of only blanks must prune to nil")
	}
}

// TestEncodeAgentsRoundTrip: what EncodeAgents writes, the live loader reads
// back field for field, in the flat form (never the retired nested container).
func TestEncodeAgentsRoundTrip(t *testing.T) {
	yes := true
	in := AgentsConfig{
		Defaults: AgentsDefaults{UseGlobalRoles: true},
		Executor: AgentBinding{Role: "agent", Command: "in-loop"},
		Reviewer: AgentBinding{Role: "reviewer", Command: "claude -p {system}", Tools: "Read", Model: "opus", Timeout: "45m", Effort: "high", Principles: "session", Env: map[string]string{"A": ""}, InjectPrinciples: &yes},
		Agents: map[string]AgentBinding{
			"coder": {Role: "agent", Interface: "acp", Command: "grok agent stdio", Secondary: "reviewer"},
			"consult": {
				Role:      "reviewer",
				Interface: "acp",
				Command:   "grok agent stdio",
				Model:     "grok-4.5",
				Env:       map[string]string{"API_TOKEN": "${API_TOKEN}"},
			},
			"empty": {},
		},
	}
	b, err := EncodeAgents(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "[agents.") {
		t.Fatalf("encoder must write the flat form:\n%s", b)
	}
	got, err := loadAgentsBody(string(b))
	if err != nil {
		t.Fatalf("encoded body does not load: %v\n%s", err, b)
	}
	if !got.Defaults.UseGlobalRoles || got.Executor.Command != "in-loop" || got.Reviewer.Model != "opus" || got.Reviewer.Timeout != "45m" {
		t.Errorf("round trip lost fields:\n%s\n%+v", b, got)
	}
	if got.Reviewer.InjectPrinciples == nil || !*got.Reviewer.InjectPrinciples {
		t.Errorf("inject_principles lost: %+v", got.Reviewer)
	}
	if _, ok := got.Reviewer.Env["A"]; !ok {
		t.Errorf("blank env value must still declare the key: %+v", got.Reviewer.Env)
	}
	if got.Agents["coder"].Interface != "acp" || got.Agents["coder"].Secondary != "reviewer" {
		t.Errorf("named binding lost fields: %+v", got.Agents["coder"])
	}
	consult := got.Agents["consult"]
	if consult.Interface != "acp" || consult.Model != "grok-4.5" || consult.Env["API_TOKEN"] != "${API_TOKEN}" {
		t.Errorf("consult binding lost secret-bearing fields: %+v", consult)
	}
	if strings.Contains(string(b), "ts_") || strings.Contains(string(b), "[vars]") {
		t.Errorf("encoding must not invent secrets or [vars]:\n%s", b)
	}
	if _, ok := got.Agents["empty"]; !ok {
		t.Errorf("an empty named table must still declare the name:\n%s", b)
	}
	b2, _ := EncodeAgents(got)
	if string(b2) != string(b) {
		t.Fatalf("encoding is not deterministic:\n%s\n---\n%s", b, b2)
	}
}
