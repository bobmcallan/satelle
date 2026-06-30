package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentsDefault(t *testing.T) {
	ac, err := LoadAgents(t.TempDir()) // no actors.toml present
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if got := ac.ReviewerBinding(); got.Tools != DefaultReviewerTools || got.Harness != DefaultReviewerHarness {
		t.Errorf("reviewer default = %+v, want tools=%q harness=%q", got, DefaultReviewerTools, DefaultReviewerHarness)
	}
	if got := ac.ExecutorBinding(); got.Harness != DefaultExecutorHarness {
		t.Errorf("executor default harness = %q, want %q", got.Harness, DefaultExecutorHarness)
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
