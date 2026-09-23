package config

import (
	"strings"
	"testing"
)

// TestExpandVarsScratchReserved: ${SATELLE_SCRATCH} is left in place (its value
// exists only per dispatch), a [vars] entry of that name is ignored, other refs
// still expand, and an unknown name still fails fast.
func TestExpandVarsScratchReserved(t *testing.T) {
	ref := "${" + ScratchEnv + "}"
	vars := map[string]string{"A": "x", ScratchEnv: "/from/vars"}
	got, err := ExpandVars(ref+"/bin", vars)
	if err != nil || got != ref+"/bin" {
		t.Fatalf("scratch ref = %q, %v", got, err)
	}
	got, err = ExpandVars("${A}:"+ref, vars)
	if err != nil || got != "x:"+ref {
		t.Fatalf("mixed = %q, %v", got, err)
	}
	if _, err := ExpandVars("${NOPE}:"+ref, vars); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("unknown var beside scratch ref = %v, want it to name NOPE", err)
	}
}

// TestResolveAgentEnvsScratchRef: wiring-time resolution accepts a binding env
// that references the reserved scratch var and keeps the reference intact.
func TestResolveAgentEnvsScratchRef(t *testing.T) {
	ref := "${" + ScratchEnv + "}/bin"
	ac := AgentsConfig{
		Agents: map[string]AgentBinding{"coder": {Env: map[string]string{"FOO": ref, "BAR": "${A}"}}},
	}
	res, err := ResolveAgentEnvs(ac, map[string]string{"A": "x"})
	if err != nil {
		t.Fatal(err)
	}
	env := res.Agents["coder"].Env
	if env["FOO"] != ref || env["BAR"] != "x" {
		t.Errorf("resolved env = %v", env)
	}
}
