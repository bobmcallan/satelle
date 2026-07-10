package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTimeoutDuration pins the per-binding dispatch bound resolution (sty_446c38b7):
// a set value parses, an empty value inherits the caller's default, and a
// malformed or non-positive value is an error.
func TestTimeoutDuration(t *testing.T) {
	def := 20 * time.Minute
	cases := []struct {
		name    string
		timeout string
		want    time.Duration
		wantErr bool
	}{
		{"set value parses", "45m", 45 * time.Minute, false},
		{"empty inherits the default", "", def, false},
		{"malformed is an error", "notaduration", 0, true},
		{"non-positive is an error", "0s", 0, true},
	}
	for _, c := range cases {
		got, err := AgentBinding{Timeout: c.timeout}.TimeoutDuration(def)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: want an error, got %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: TimeoutDuration = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestLoadAgentsFailsFastOnBadTimeout pins AC1: a malformed [<section>] timeout is
// rejected at LOAD, naming the offending section — not deferred to dispatch time.
func TestLoadAgentsFailsFastOnBadTimeout(t *testing.T) {
	dir := t.TempDir()
	body := "[worker]\nharness = \"claude {system}\"\ntimeout = \"nope\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAgents(dir)
	if err == nil {
		t.Fatal("LoadAgents must reject a malformed binding timeout")
	}
	if !strings.Contains(err.Error(), "worker") || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error should name the [worker] timeout, got: %v", err)
	}

	// A well-formed timeout loads and is readable on the binding.
	ok := "[worker]\nharness = \"claude {system}\"\ntimeout = \"45m\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(ok), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("a valid timeout should load: %v", err)
	}
	if b := ac.Agents["worker"]; b.Timeout != "45m" {
		t.Errorf("worker binding timeout = %q, want 45m", b.Timeout)
	}
}

func TestLoadAgentsDefault(t *testing.T) {
	ac, err := LoadAgents(t.TempDir()) // no actors.toml present
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != DefaultReviewerTools || got.Command != DefaultReviewerCommand {
		t.Errorf("reviewer default = %+v, want tools=%q harness=%q", got, DefaultReviewerTools, DefaultReviewerCommand)
	}
	if got := ac.ExecutorBinding(); got.Command != DefaultExecutorCommand {
		t.Errorf("executor default harness = %q, want %q", got.Command, DefaultExecutorCommand)
	}
}

// TestLoadAgentsOnlyAgentsToml proves the loader reads agents.toml and that the
// legacy actors.toml is NO LONGER loaded (sty_7db2ed7d): a repo carrying only the
// retired filename resolves to defaults (the zero config), not its bindings.
func TestLoadAgentsOnlyAgentsToml(t *testing.T) {
	// Canonical agents.toml: loads.
	canon := t.TempDir()
	if err := os.WriteFile(filepath.Join(canon, AgentsConfigName), []byte("[reviewer]\nmodel = \"canon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadAgents(canon); err != nil || ac.Reviewer.Model != "canon" {
		t.Fatalf("canonical agents.toml: ac=%+v err=%v", ac, err)
	}

	// Legacy actors.toml only: NOT loaded — resolves to the zero config.
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, ActorsConfigName), []byte("[reviewer]\nmodel = \"legacy\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ac, err := LoadAgents(legacy); err != nil || ac.Reviewer.Model != "" {
		t.Fatalf("legacy actors.toml must not load: ac=%+v err=%v", ac, err)
	}
}

// TestNamedBinding proves a named agent declared under [agents.<name>] resolves as
// an isolated binding, and that an undeclared name reports ok=false so the caller
// falls back to the in-loop executor (sty_b2222b8a).
func TestNamedBinding(t *testing.T) {
	dir := t.TempDir()
	body := "[agents.commit-agent]\nharness = \"claude -p --allowedTools {tools}\"\ntools = \"Read,Bash(git:*)\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	b, ok := ac.NamedBinding("commit-agent")
	if !ok || b.Tools != "Read,Bash(git:*)" || b.Harness != "claude -p --allowedTools {tools}" {
		t.Errorf("commit-agent binding = %+v ok=%v, want the declared harness+tools", b, ok)
	}
	if _, ok := ac.NamedBinding("nope"); ok {
		t.Error("an undeclared named agent must report ok=false (fall back to in-loop)")
	}
}

// TestFlatNamedAgent proves a named agent declared in the FLAT top-level form
// [<name>] resolves as a named isolated agent, while [executor]/[reviewer] in the
// same file remain the built-in roles (not named agents) — sty_6e0ba71c.
func TestFlatNamedAgent(t *testing.T) {
	dir := t.TempDir()
	body := "[reviewer]\nmodel = \"sonnet\"\n" +
		"[commit-agent]\nharness = \"claude -p --allowedTools {tools}\"\ntools = \"Read,Bash(git:*)\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	// Flat [commit-agent] resolves as a named agent.
	b, ok := ac.NamedBinding("commit-agent")
	if !ok || b.Tools != "Read,Bash(git:*)" || b.Harness != "claude -p --allowedTools {tools}" {
		t.Errorf("flat [commit-agent] = %+v ok=%v, want the declared harness+tools", b, ok)
	}
	// [reviewer] stays a built-in ROLE, not a named agent.
	if ac.Reviewer.Model != "sonnet" {
		t.Errorf("reviewer role model = %q, want sonnet", ac.Reviewer.Model)
	}
	if _, ok := ac.NamedBinding("reviewer"); ok {
		t.Error("[reviewer] is a built-in role, must NOT be a named agent")
	}
	if _, ok := ac.NamedBinding("executor"); ok {
		t.Error("[executor] is a built-in role, must NOT be a named agent")
	}
}

func TestLoadAgentsOverride(t *testing.T) {
	dir := t.TempDir()
	body := "[reviewer]\ntools = \"Read\"\nharness = \"other-harness\"\n[executor]\nharness = \"claude -p\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != "Read" || got.Harness != "other-harness" {
		t.Errorf("reviewer override = %+v, want tools=Read harness=other-harness", got)
	}
	if got := ac.ExecutorBinding(); got.Harness != "claude -p" {
		t.Errorf("executor override harness = %q, want claude -p", got.Harness)
	}
}

// TestLoadAgentsSettingsTable pins AC1/AC2: a binding's `settings` table decodes
// as a generic map[string]any (env, model, and a nested permissions.allow array)
// with no dedicated schema struct — LoadAgents's existing PrimitiveDecode-into-map
// path already handles a nested table for free. A binding with no settings table
// yields a nil map (not an empty-but-present one), so it materialises to no
// {settings} value at all.
func TestLoadAgentsSettingsTable(t *testing.T) {
	dir := t.TempDir()
	body := "[worker]\n" +
		"harness = \"claude {system} --settings {settings}\"\n" +
		"[worker.settings]\n" +
		"model = \"glm-4.6\"\n" +
		"[worker.settings.env]\n" +
		"ANTHROPIC_BASE_URL = \"https://api.z.ai/api/anthropic\"\n" +
		"ANTHROPIC_AUTH_TOKEN = \"${GLM_API_KEY}\"\n" +
		"[worker.settings.permissions]\n" +
		"allow = [\"Read\", \"Bash(git:*)\"]\n" +
		"[reviewer]\n" +
		"model = \"sonnet\"\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	b, ok := ac.NamedBinding("worker")
	if !ok {
		t.Fatal("worker binding not found")
	}
	if got := b.Settings["model"]; got != "glm-4.6" {
		t.Errorf("settings.model = %v, want glm-4.6", got)
	}
	env, ok := b.Settings["env"].(map[string]any)
	if !ok {
		t.Fatalf("settings.env = %T, want map[string]any", b.Settings["env"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.z.ai/api/anthropic" {
		t.Errorf("settings.env.ANTHROPIC_BASE_URL = %v", env["ANTHROPIC_BASE_URL"])
	}
	perms, ok := b.Settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("settings.permissions = %T, want map[string]any", b.Settings["permissions"])
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) != 2 || allow[0] != "Read" || allow[1] != "Bash(git:*)" {
		t.Errorf("settings.permissions.allow = %v", perms["allow"])
	}
	// A binding with no settings table has a nil map.
	if ac.Reviewer.Settings != nil {
		t.Errorf("reviewer settings = %v, want nil (no settings table declared)", ac.Reviewer.Settings)
	}
}

func TestInjectPrinciplesDefaultsOnAndToggles(t *testing.T) {
	// Absent from agents.toml → default ON.
	if !(AgentBinding{}).InjectsPrinciples() {
		t.Error("an unset binding must inject principles by default")
	}
	dir := t.TempDir()
	body := "[reviewer]\ninject_principles = false\n[commit-agent]\ninject_principles = true\n"
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if ac.ReviewerBinding().InjectsPrinciples() {
		t.Error("inject_principles = false must disable injection for the reviewer")
	}
	if nb, ok := ac.NamedBinding("commit-agent"); !ok || !nb.InjectsPrinciples() {
		t.Errorf("named agent with inject_principles = true must inject: ok=%v binding=%+v", ok, nb)
	}
}
