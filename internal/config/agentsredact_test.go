package config

import (
	"strings"
	"testing"
)

const unredactedAgents = `
[defaults]
secondary = "reviewer"

[executor]
role = "agent"
command = "in-loop"

[reviewer]
role       = "reviewer"
profile    = "claude-opus"
command    = "/opt/local/bin/claude -p --output-format json --append-system-prompt {system} --model {model}"
tools      = "Read,Grep,Glob"
model      = "opus"
effort     = "high"
env        = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}", ANTHROPIC_BASE_URL = "https://api.example.test" }
settings   = { model = "opus", apiKey = "sk-live-123", env = { CLAUDE_CODE_TOKEN = "t0k" }, permissions = { allow = ["Read"], additionalDirectories = ["/home/someone/proj", "relative/dir"] } }

[coder]
role      = "agent"
interface = "stream"
command   = "/home/someone/.local/bin/claude -p --input-format stream-json --output-format stream-json --allowedTools {tools}"
tools     = "Read,Edit,Bash(satelle:*)"
`

// TestRedactAgentsTransport pins the transport contract (sty_01949949 AC1/R1):
// env KEYS survive with blank values, absolute command tokens become base
// names, profile is dropped, secret-shaped settings and settings.env values are
// blanked, and everything else — roles, tools, models, placeholders — is intact.
func TestRedactAgentsTransport(t *testing.T) {
	out, err := RedactAgentsTransport([]byte(unredactedAgents))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, leak := range []string{"https://api.example.test", "/opt/local/bin", "/home/someone", "sk-live-123", "t0k", "claude-opus", "profile"} {
		if strings.Contains(s, leak) {
			t.Errorf("redacted body still carries %q:\n%s", leak, s)
		}
	}
	// A pure ${VAR} reference is a variable NAME, not a value: it travels, so the
	// receiving machine's [vars] fail-fast can fire when the value is missing.
	for _, keep := range []string{"${GLM_API_KEY}", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "claude -p --output-format json", "{system}", "{model}", "[coder]", `interface = "stream"`, "Read,Edit,Bash(satelle:*)", `secondary = "reviewer"`, `role = "reviewer"`, "CLAUDE_CODE_TOKEN"} {
		if !strings.Contains(s, keep) {
			t.Errorf("redacted body lost %q:\n%s", keep, s)
		}
	}
	// The result must still LOAD, in the same flat classification.
	ac, err := loadAgentsBody(s)
	if err != nil {
		t.Fatalf("redacted body does not load: %v\n%s", err, s)
	}
	rb := ac.Reviewer
	if rb.Env["ANTHROPIC_AUTH_TOKEN"] != "${GLM_API_KEY}" {
		t.Errorf("a ${VAR} reference must survive redaction: %+v", rb.Env)
	}
	if v, ok := rb.Env["ANTHROPIC_BASE_URL"]; !ok || v != "" {
		t.Errorf("a literal env value must be blanked with its key kept: %+v", rb.Env)
	}
	if !strings.HasPrefix(rb.Command, "claude -p") {
		t.Errorf("command must keep the base name: %q", rb.Command)
	}
	if rb.Profile != "" {
		t.Errorf("profile must be dropped: %q", rb.Profile)
	}
	if rb.Settings["model"] != "opus" {
		t.Errorf("non-secret settings must survive: %+v", rb.Settings)
	}
	if rb.Settings["apiKey"] != "" {
		t.Errorf("secret-shaped settings key must be blanked: %+v", rb.Settings)
	}
	if envTbl, _ := rb.Settings["env"].(map[string]any); envTbl["CLAUDE_CODE_TOKEN"] != "" {
		t.Errorf("settings.env values must be blanked: %+v", rb.Settings)
	}
	perms, _ := rb.Settings["permissions"].(map[string]any)
	dirs, _ := perms["additionalDirectories"].([]any)
	if len(dirs) != 2 || dirs[0] != "proj" || dirs[1] != "relative/dir" {
		t.Errorf("absolute paths inside settings lists must reduce to base names: %+v", perms)
	}
	if c := ac.Agents["coder"]; !strings.HasPrefix(c.Command, "claude -p --input-format stream-json") || c.Interface != "stream" {
		t.Errorf("named binding not carried: %+v", c)
	}
	if ac.Defaults.Secondary != "reviewer" {
		t.Errorf("defaults must survive: %+v", ac.Defaults)
	}
	// Idempotent: redacting the redacted body is a byte-identical no-op, so a
	// re-push of unchanged config does not create a new catalog version.
	again, err := RedactAgentsTransport(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != s {
		t.Fatalf("redaction is not idempotent:\n--- first\n%s\n--- second\n%s", s, again)
	}
}

// TestRehydrateAgentsKeepsLocalWhenStoreMatches: a store copy that is exactly
// the local file after redaction holds nothing the repo lacks — the authored
// bytes (comments included) are left alone.
func TestRehydrateAgentsKeepsLocalWhenStoreMatches(t *testing.T) {
	local := "# authored, with a comment\n" + unredactedAgents
	store, err := RedactAgentsTransport([]byte(local))
	if err != nil {
		t.Fatal(err)
	}
	out, keep, err := RehydrateAgents(store, []byte(local))
	if err != nil {
		t.Fatal(err)
	}
	if !keep || out != nil {
		t.Fatalf("matching store must keep the local file: keep=%v out=%q", keep, out)
	}
}

// TestRehydrateAgentsMergesLocalValuesUnderStoreLayout: when the store differs
// (a deploy that actually changes something), its layout wins and everything
// redaction removed comes back from the local file — env values, absolute
// command paths, profile=, secret settings — so live bindings are not blanked.
func TestRehydrateAgentsMergesLocalValuesUnderStoreLayout(t *testing.T) {
	// The store copy: same bindings, redacted, but the reviewer model changed
	// upstream and a new binding was added.
	changed := strings.Replace(unredactedAgents, `model      = "opus"`, `model      = "sonnet"`, 1) + "\n[summariser]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	store, err := RedactAgentsTransport([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	out, keep, err := RehydrateAgents(store, []byte(unredactedAgents))
	if err != nil {
		t.Fatal(err)
	}
	if keep {
		t.Fatal("a differing store must be deployed, not skipped")
	}
	got, err := loadAgentsBody(string(out))
	if err != nil {
		t.Fatalf("merged body does not load: %v\n%s", err, out)
	}
	rb := got.Reviewer
	if rb.Model != "sonnet" {
		t.Errorf("store layout must win: model = %q", rb.Model)
	}
	if rb.Env["ANTHROPIC_AUTH_TOKEN"] != "${GLM_API_KEY}" || rb.Env["ANTHROPIC_BASE_URL"] != "https://api.example.test" {
		t.Errorf("local env values must be re-applied: %+v", rb.Env)
	}
	if !strings.HasPrefix(rb.Command, "/opt/local/bin/claude -p") {
		t.Errorf("local absolute command path must be re-applied: %q", rb.Command)
	}
	if rb.Profile != "claude-opus" {
		t.Errorf("local profile= must be re-applied: %q", rb.Profile)
	}
	if rb.Settings["apiKey"] != "sk-live-123" {
		t.Errorf("local secret settings must be re-applied: %+v", rb.Settings)
	}
	if envTbl, _ := rb.Settings["env"].(map[string]any); envTbl["CLAUDE_CODE_TOKEN"] != "t0k" {
		t.Errorf("local settings.env must be re-applied: %+v", rb.Settings)
	}
	perms, _ := rb.Settings["permissions"].(map[string]any)
	if dirs, _ := perms["additionalDirectories"].([]any); len(dirs) != 2 || dirs[0] != "/home/someone/proj" {
		t.Errorf("local absolute paths in settings lists must be re-applied: %+v", perms)
	}
	if c := got.Agents["coder"]; !strings.HasPrefix(c.Command, "/home/someone/.local/bin/claude") {
		t.Errorf("named binding command path must be re-applied: %+v", c)
	}
	if _, ok := got.Agents["summariser"]; !ok {
		t.Error("a binding only the store has must arrive")
	}
	// No local file at all: the store copy is deployed as-is.
	out2, keep2, err := RehydrateAgents(store, nil)
	if err != nil || keep2 || string(out2) != string(store) {
		t.Fatalf("no local file: out=%q keep=%v err=%v", out2, keep2, err)
	}
	// An unparseable local file cannot be merged.
	if _, _, err := RehydrateAgents(store, []byte("[reviewer\nbroken")); err == nil {
		t.Fatal("unparseable local file must be an error for the caller to report")
	}
}

func TestIsVarRef(t *testing.T) {
	for v, want := range map[string]bool{"${TOKEN}": true, " ${a_1} ": true, "": false, "literal": false, "Bearer ${TOKEN}": false, "${}": false, "${1x}": false} {
		if got := IsVarRef(v); got != want {
			t.Errorf("IsVarRef(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestPruneUnsatisfied: a blank left by redaction is "declared, unsatisfied" —
// dropped from the binding — while references and real values stay.
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

func TestRedactAgentsTransportRefusesUnparseable(t *testing.T) {
	if _, err := RedactAgentsTransport([]byte("[reviewer\ncommand = ")); err == nil {
		t.Fatal("an unparseable agents body must be an error, never passed through raw")
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
		t.Errorf("redaction must not invent secrets or [vars]:\n%s", b)
	}
	if _, ok := got.Agents["empty"]; !ok {
		t.Errorf("an empty named table must still declare the name:\n%s", b)
	}
	b2, _ := EncodeAgents(got)
	if string(b2) != string(b) {
		t.Fatalf("encoding is not deterministic:\n%s\n---\n%s", b, b2)
	}
}
