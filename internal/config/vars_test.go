package config

import (
	"strings"
	"testing"
)

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"GLM_API_KEY": "sk-secret", "REGION": "us"}

	// Happy path + multiple refs in one value.
	got, err := ExpandVars("${GLM_API_KEY}", vars)
	if err != nil || got != "sk-secret" {
		t.Fatalf("single ref = %q, %v", got, err)
	}
	got, err = ExpandVars("https://${REGION}.example/${GLM_API_KEY}", vars)
	if err != nil || got != "https://us.example/sk-secret" {
		t.Fatalf("multi ref = %q, %v", got, err)
	}

	// Passthrough: a value with no ${...} ref, a bare $FOO, a bare {model}, and an
	// unterminated ${ all pass verbatim (only the exact ${NAME} form is a KV ref).
	for _, s := range []string{"plain", "$FOO", "{model}", "${unterminated", "cost is $5"} {
		got, err := ExpandVars(s, vars)
		if err != nil || got != s {
			t.Errorf("passthrough %q = %q, %v", s, got, err)
		}
	}

	// Unknown var → error naming the var, never a silent empty.
	if _, err := ExpandVars("${MISSING}", vars); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("unknown var error = %v, want it to name MISSING", err)
	}
}

// TestExpandVarsNamespace pins AC3: a var literally named "model" is addressable
// as ${model} in an env value, while the {model} argv placeholder is untouched
// (ExpandVars only ever sees env values, never a harness argv token).
func TestExpandVarsNamespace(t *testing.T) {
	vars := map[string]string{"model": "glm-4.6"}
	got, err := ExpandVars("${model}", vars)
	if err != nil || got != "glm-4.6" {
		t.Fatalf("${model} = %q, %v", got, err)
	}
	// The bare-brace argv form is NOT a KV ref — it passes through untouched.
	got, err = ExpandVars("--model {model}", vars)
	if err != nil || got != "--model {model}" {
		t.Fatalf("bare {model} should pass through, got %q, %v", got, err)
	}
}

func TestResolveAgentEnvs(t *testing.T) {
	vars := map[string]string{"GLM_API_KEY": "sk-secret"}
	ac := AgentsConfig{
		Reviewer: AgentBinding{Model: "sonnet"},
		Agents: map[string]AgentBinding{
			"planner": {Env: map[string]string{
				"ANTHROPIC_BASE_URL":   "https://api.z.ai/api/anthropic",
				"ANTHROPIC_AUTH_TOKEN": "${GLM_API_KEY}",
			}},
		},
	}
	res, err := ResolveAgentEnvs(ac, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Agents["planner"].Env["ANTHROPIC_AUTH_TOKEN"]; got != "sk-secret" {
		t.Errorf("resolved token = %q, want sk-secret", got)
	}
	if got := res.Agents["planner"].Env["ANTHROPIC_BASE_URL"]; got != "https://api.z.ai/api/anthropic" {
		t.Errorf("base url mangled: %q", got)
	}
	// The original binding is not mutated (a copy is returned).
	if ac.Agents["planner"].Env["ANTHROPIC_AUTH_TOKEN"] != "${GLM_API_KEY}" {
		t.Errorf("source binding mutated: %v", ac.Agents["planner"].Env)
	}
	// A binding with no env is a no-op (zero-config stays zero-config).
	if res.Reviewer.Env != nil {
		t.Errorf("empty env should stay nil, got %v", res.Reviewer.Env)
	}
}

// TestResolveAgentEnvsUnknownVar pins AC1: an unknown ${VAR} refuses, and the
// error names the binding section + env key + missing var but NEVER a value.
func TestResolveAgentEnvsUnknownVar(t *testing.T) {
	ac := AgentsConfig{
		Agents: map[string]AgentBinding{
			"planner": {Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "${GLM_API_KEY}"}},
		},
	}
	_, err := ResolveAgentEnvs(ac, nil)
	if err == nil {
		t.Fatal("want refusal for unknown var")
	}
	msg := err.Error()
	for _, want := range []string{"planner", "ANTHROPIC_AUTH_TOKEN", "GLM_API_KEY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}
