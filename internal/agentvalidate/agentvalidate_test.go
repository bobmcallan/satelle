package agentvalidate

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

func TestValidate_Healthy(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus"},
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

// TestValidate_GateBindingSection (sty_a476a2f8): effective model is the binding
// that will run the gate; named gate agent= resolves that binding; model= is ignored.
func TestValidate_GateBindingSection(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"planner":       {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "sonnet", Role: "agent"},
			"reviewer-deep": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus", Role: "reviewer"},
		},
	}
	wfs := []docindex.Doc{{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\n---\n```dot\ndigraph w {\n" +
			"  backlog [shape=Mdiamond]\n" +
			"  plan [agent=planner, prompt=\"@skill:plan\"]\n" +
			"  done [shape=Msquare]\n" +
			"  estimate [agent=reviewer, prompt=\"@skill:est\", on=\"done\"]\n" +
			"  backlog -> plan [agent=reviewer-deep, prompt=\"@skill:intent\"]\n" +
			"  plan -> done [agent=reviewer, prompt=\"@skill:close\"]\n" +
			"}\n```\n",
	}}
	r := Validate(agents, nil, wfs)
	if !r.OK() {
		t.Fatalf("problems: %v", r.Problems)
	}
	by := map[string]GateAllocation{}
	for _, g := range r.Gates {
		key := g.Node + "|" + g.Skill
		by[key] = g
	}
	edge := by["edge:backlog→plan|intent"]
	if edge.Agent != "reviewer-deep" || edge.EffectiveModel != "opus" {
		t.Errorf("edge gate = %+v, want agent=reviewer-deep model=opus", edge)
	}
	closeG := by["edge:plan→done|close"]
	if closeG.Agent != "reviewer" || closeG.EffectiveModel != "grok-4.5" {
		t.Errorf("close edge = %+v, want [reviewer]/grok-4.5", closeG)
	}
	plan := by["plan|plan"]
	if plan.Agent != "planner" || plan.EffectiveModel != "sonnet" {
		t.Errorf("plan node = %+v, want planner/sonnet", plan)
	}
	est := by["estimate|est"]
	if est.EffectiveModel != "grok-4.5" {
		t.Errorf("estimate scoped = %+v, want grok-4.5", est)
	}
	// sty_6ab016dc: named role=reviewer on a gated edge is live — no stale WARN
	// claiming it is a "named perform binding" or that gates fall back to [reviewer].
	for _, w := range r.Warnings {
		if strings.Contains(w, "named perform") || strings.Contains(w, "gates use [reviewer] by default") {
			t.Errorf("stale contradictory WARN must not fire when NODE names a named reviewer: %s", w)
		}
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

func TestValidate_BarePresetRejected(t *testing.T) {
	for _, bare := range []string{"claude", "grok", "codex"} {
		agents := config.AgentsConfig{
			Executor: config.AgentBinding{Command: "in-loop"},
			Reviewer: config.AgentBinding{Command: bare},
		}
		r := Validate(agents, nil, nil)
		if r.OK() {
			t.Fatalf("bare %q must be flagged", bare)
		}
		joined := strings.Join(r.Problems, "\n")
		if !strings.Contains(joined, "bare CLI presets removed") {
			t.Errorf("bare %q: expected bare-preset problem:\n%s", bare, joined)
		}
		for _, g := range r.Grants {
			if g.Name == "reviewer" && g.Backend != "invalid" {
				t.Errorf("bare %q: reviewer backend = %q, want invalid", bare, g.Backend)
			}
		}
	}
	// in-loop and empty stay valid (executor path).
	ok := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
	}
	if r := Validate(ok, nil, nil); !r.OK() {
		t.Fatalf("full template + in-loop must pass: %v", r.Problems)
	}
	// Omitted [reviewer] command resolves to full DefaultClaudeCommand (AC2).
	omit := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		// Reviewer empty — ReviewerBinding fills DefaultReviewerCommand
	}
	// Validate receives raw config; empty command is treated as in-loop path
	// unless we resolve via ReviewerBinding. Mirror how the runtime resolves:
	resolved := omit
	rb := omit.ReviewerBinding()
	resolved.Reviewer = rb
	if rb.Command != agentcli.DefaultClaudeCommand {
		t.Fatalf("omitted reviewer command resolved to %q, want DefaultClaudeCommand", rb.Command)
	}
	if r := Validate(resolved, nil, nil); !r.OK() {
		t.Fatalf("resolved omitted reviewer must pass: %v", r.Problems)
	}
}

// TestValidate_MissingSystemPlaceholder (sty_21db3670 AC1): a multi-token
// isolated command that omits {system} as its own argv token is a hard problem.
func TestValidate_MissingSystemPlaceholder(t *testing.T) {
	// Full-looking claude template with no {system} token — rubric never appended.
	noSystem := "claude -p --output-format json --allowedTools {tools} --model {model}"
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: noSystem, Tools: "Read,Grep,Glob"},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("command missing {system} must produce a problem")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "{system}") || !strings.Contains(joined, "reviewer") {
		t.Errorf("problem should name {system} and the section:\n%s", joined)
	}
	// Fused form (placeholder not its own token) also fails — mirrors buildArgs.
	fused := "claude -p --append-system-prompt={system} --allowedTools {tools}"
	agents.Reviewer.Command = fused
	r2 := Validate(agents, nil, nil)
	if r2.OK() {
		t.Fatal("fused {system} (not own argv token) must produce a problem")
	}
	// Present {system} as own token passes (no system-placeholder problem).
	ok := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
	}
	r3 := Validate(ok, nil, nil)
	if !r3.OK() {
		t.Fatalf("DefaultClaudeCommand must pass: %v", r3.Problems)
	}
	for _, p := range r3.Problems {
		if strings.Contains(p, "{system}") {
			t.Errorf("DefaultClaudeCommand must not flag {system}: %s", p)
		}
	}
}

// TestValidate_ReviewerNoCeiling (sty_21db3670 AC2): role=reviewer isolated
// command with no read-only ceiling is a WARNING (not a hard problem).
func TestValidate_ReviewerNoCeiling(t *testing.T) {
	// Has {system}, grants Write/Edit, no --disallowedTools / --deny.
	writable := "claude -p --append-system-prompt {system} --allowedTools Read,Write,Edit --model {model}"
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: writable, Tools: "Read,Write,Edit", Role: "reviewer"},
	}
	r := Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("missing ceiling is a warning, not a problem: %v", r.Problems)
	}
	joined := strings.Join(r.Warnings, "\n")
	if !strings.Contains(joined, "reviewer") || !strings.Contains(joined, "read-only ceiling") {
		t.Errorf("expected ceiling warning naming reviewer:\n%s", joined)
	}
}

// TestValidate_PlaceholderHealthyUnchanged (sty_21db3670 AC3): in-loop + canonical
// full templates produce neither a {system} problem nor a ceiling warning.
func TestValidate_PlaceholderHealthyUnchanged(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus"},
		},
	}
	r := Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("canonical templates must pass: %v", r.Problems)
	}
	for _, p := range r.Problems {
		if strings.Contains(p, "{system}") {
			t.Errorf("unexpected {system} problem: %s", p)
		}
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "read-only ceiling") {
			t.Errorf("canonical reviewer must not warn about ceiling: %s", w)
		}
	}
}

func TestValidate_UnresolvedEnv(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{
			Command: agentcli.DefaultClaudeCommand,
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
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
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
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
		Agents: map[string]config.AgentBinding{
			"unused-agent": {Command: agentcli.DefaultClaudeCommand},
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

// TestValidate_OnEnterAgentBinding: on_enter_agent counts as a use of the named
// binding (not orphaned) and hard-fails when the binding is missing (sty_5cabe26f).
func TestValidate_OnEnterAgentBinding(t *testing.T) {
	wfs := []docindex.Doc{{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\n---\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  parked [agent=reviewer, prompt=\"@skill:park\", on_enter_agent=triage, on_enter_prompt=\"@skill:triage\"]\n  done [shape=Msquare]\n  backlog -> parked -> done\n}\n```\n",
	}}
	// Matching binding → OK, not orphaned.
	okAgents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
		Agents: map[string]config.AgentBinding{
			"triage": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Bash(satelle:*)"},
		},
	}
	r := Validate(okAgents, nil, wfs)
	if !r.OK() {
		t.Fatalf("on_enter with matching binding must be OK: %v", r.Problems)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "triage") && strings.Contains(w, "orphan") {
			t.Errorf("on_enter_agent must mark the binding used, got orphan warning: %s", w)
		}
	}
	// Missing binding → hard problem.
	missing := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand},
	}
	r2 := Validate(missing, nil, wfs)
	if r2.OK() {
		t.Fatal("on_enter_agent without binding must produce a problem")
	}
	joined := strings.Join(r2.Problems, "\n")
	if !strings.Contains(joined, "on_enter_agent=triage") || !strings.Contains(joined, "parked") {
		t.Errorf("problem should name on_enter_agent and node:\n%s", joined)
	}
}

func TestValidate_BadTimeout(t *testing.T) {
	// LoadAgents would refuse this at load; Validate still checks TimeoutDuration
	// on the in-memory binding for callers that construct AgentsConfig directly.
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Timeout: "not-a-duration"},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("bad timeout must produce a problem")
	}
	if !strings.Contains(strings.Join(r.Problems, "\n"), "timeout") {
		t.Errorf("problem should mention timeout: %v", r.Problems)
	}
}

func TestValidate_ACPInterface(t *testing.T) {
	// Placeholders in acp command → FAIL.
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{
			Interface: "acp",
			Command:   "grok agent stdio {system}",
			Tools:     "read_file,grep,list_dir",
			Role:      "reviewer",
			Model:     "grok-4.5",
		},
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("acp with {system} must fail validate")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "placeholder") && !strings.Contains(joined, "{system}") {
		t.Errorf("problems should mention placeholder, got: %s", joined)
	}

	// Valid acp reviewer.
	agents.Reviewer = config.AgentBinding{
		Interface: "acp",
		Command:   "grok agent stdio",
		Tools:     "read_file,grep,list_dir",
		Role:      "reviewer",
		Model:     "grok-4.5",
	}
	r = Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("valid acp reviewer problems: %v", r.Problems)
	}
	var g Grant
	for _, x := range r.Grants {
		if x.Name == "reviewer" {
			g = x
		}
	}
	if g.Interface != "acp" || !strings.HasPrefix(g.Backend, "acp:") {
		t.Errorf("grant = %+v, want interface=acp backend acp:*", g)
	}
	if !g.ReadOnly {
		t.Error("acp reviewer with read-only tools should be ReadOnly")
	}

	// Existing command path still healthy with no interface key.
	agents.Reviewer = config.AgentBinding{
		Command: agentcli.DefaultGrokCommand,
		Tools:   "read_file,grep,list_dir",
		Model:   "grok-4.5",
	}
	r = Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("command default must still validate: %v", r.Problems)
	}
}

func TestValidate_GrantEffort(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{
			Command: agentcli.DefaultGrokCommand,
			Tools:   "read_file,grep,list_dir",
			Model:   "grok-4.5",
			Effort:  "high",
		},
	}
	r := Validate(agents, nil, nil)
	var rev Grant
	for _, g := range r.Grants {
		if g.Name == "reviewer" {
			rev = g
		}
	}
	if rev.Effort != "high" {
		t.Fatalf("reviewer grant effort = %q, want high", rev.Effort)
	}
}
