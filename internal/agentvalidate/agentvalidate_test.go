package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

func TestValidate_Healthy(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "grok", Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"planner": {Command: "claude", Tools: "Read,Grep,Glob", Model: "opus"},
		},
	}
	wfs := []docindex.Doc{{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\n---\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  plan [agent=planner, prompt=\"@skill:plan\"]\n  done [shape=Msquare]\n  backlog -> plan -> done\n}\n```\n",
	}}
	r := Validate(agents, nil, wfs)
	if !r.OK() {
		t.Fatalf("healthy fixture must have no problems: %v", r.Problems)
	}
	byName := map[string]Grant{}
	for _, g := range r.Grants {
		byName[g.Name] = g
	}
	if byName["executor"].Backend != "in-loop" {
		t.Errorf("executor backend = %q, want in-loop", byName["executor"].Backend)
	}
	if !strings.HasPrefix(byName["reviewer"].Backend, "isolated:") {
		t.Errorf("reviewer backend = %q, want isolated:*", byName["reviewer"].Backend)
	}
	if byName["reviewer"].ReadOnly != true {
		t.Errorf("reviewer should be read-only")
	}
	if byName["planner"].Backend != "isolated:claude" {
		t.Errorf("planner backend = %q, want isolated:claude", byName["planner"].Backend)
	}
}

func TestValidate_BrokenBinding(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "not-a-cli"},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("unknown preset must produce a problem")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "reviewer") {
		t.Errorf("problem should name the section:\n%s", joined)
	}
}

func TestValidate_CodexUnmapped(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "codex"},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("bare codex preset must be flagged")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "codex") || !strings.Contains(joined, "not yet mapped") {
		t.Errorf("expected codex unmapped problem:\n%s", joined)
	}
	for _, g := range r.Grants {
		if g.Name == "reviewer" && g.Backend != "codex (unmapped)" {
			t.Errorf("reviewer backend = %q, want codex (unmapped)", g.Backend)
		}
	}
}

func TestValidate_UnresolvedEnv(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{
			Command: "claude",
			Env:     map[string]string{"TOKEN": "${MISSING_VAR}"},
		},
	}
	r := Validate(agents, map[string]string{}, nil)
	if r.OK() {
		t.Fatal("unresolved env var must produce a problem")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "MISSING_VAR") && !strings.Contains(joined, "reviewer") {
		t.Errorf("problem should name the unresolved var / section:\n%s", joined)
	}
	// Never leak a secret-shaped value — the raw ${…} may appear but no fabricated secret.
	if strings.Contains(joined, "sk-") {
		t.Errorf("problems must not leak secret-like values:\n%s", joined)
	}
}

func TestValidate_MissingNodeBinding(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "claude"},
	}
	wfs := []docindex.Doc{{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\n---\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  work [agent=ghost, prompt=\"@skill:code\"]\n  done [shape=Msquare]\n  backlog -> work -> done\n}\n```\n",
	}}
	r := Validate(agents, nil, wfs)
	if r.OK() {
		t.Fatal("missing node binding must produce a problem")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "ghost") || !strings.Contains(joined, "work") {
		t.Errorf("problem should name agent and node:\n%s", joined)
	}
}

func TestValidate_OrphanBinding(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "claude"},
		Agents: map[string]config.AgentBinding{
			"unused-agent": {Command: "claude"},
		},
	}
	wfs := []docindex.Doc{{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\n---\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare]\n  backlog -> done\n}\n```\n",
	}}
	r := Validate(agents, nil, wfs)
	// Orphans are warnings (advisory) — [retrospective]-style non-workflow agents
	// must not hard-fail engage/init.
	if !r.OK() {
		t.Fatalf("orphan must not be a hard problem: %v", r.Problems)
	}
	joined := strings.Join(r.Warnings, "\n")
	if !strings.Contains(joined, "unused-agent") || !strings.Contains(joined, "orphan") {
		t.Errorf("warning should name the orphan:\n%s", joined)
	}
}

func TestValidate_BadTimeout(t *testing.T) {
	// LoadAgents would refuse this at load; Validate still checks TimeoutDuration
	// on the in-memory binding for callers that construct AgentsConfig directly.
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: "claude", Timeout: "not-a-duration"},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("bad timeout must produce a problem")
	}
	if !strings.Contains(strings.Join(r.Problems, "\n"), "timeout") {
		t.Errorf("problem should mention timeout: %v", r.Problems)
	}
}
