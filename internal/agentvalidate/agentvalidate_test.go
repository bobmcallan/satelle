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
			"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "opus"},
		},
	}
	wfs := routeDocs(
		`["*"]
obligations = ["raised", "planned", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
skills = ["plan"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["planned"]
`)
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
			"planner":       {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "sonnet", Role: "agent"},
			"reviewer-deep": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus", Role: "reviewer"},
		},
	}
	wfs := routeDocs(
		`["*"]
obligations = ["raised", "planned", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["intent"]
reviewer_agent = "reviewer-deep"
requires = ["raised"]

[closed]
status = "done"
reviewers = ["close"]
reviewer_agent = "reviewer"
terminal = true
requires = ["planned"]

[[gate]]
skill = "est"
agent = "reviewer"
on = ["done"]
`)
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
	// A derived route names an always-on gate node for its skill.
	est := by["gate_est|est"]
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

// TestValidate_StepSummaryNamedReviewer (sty_8ee40f94): step-summary may allocate
// a named role=reviewer (cheap summariser); not a performer, not orphaned.
func TestValidate_StepSummaryNamedReviewer(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "opus"},
		Agents: map[string]config.AgentBinding{
			"reviewer-summary": {
				Command: "grok agent stdio", Role: "reviewer", Interface: "acp",
				Tools: "read_file,grep,list_dir", Model: "grok-4.5", Effort: "low",
			},
		},
	}
	wfNamed := routeDocs(
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[[gate]]
skill = "satelle-step-summary"
agent = "reviewer-summary"
mandatory = true
`)
	r := Validate(agents, nil, wfNamed)
	if !r.OK() {
		t.Fatalf("named step-summary reviewer must pass: %v", r.Problems)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "reviewer-summary") && strings.Contains(w, "orphaned") {
			t.Errorf("reviewer-summary must not be orphaned: %s", w)
		}
	}
	found := false
	for _, g := range r.Gates {
		if g.Skill == "satelle-step-summary" {
			found = true
			if g.Agent != "reviewer-summary" || g.EffectiveModel != "grok-4.5" {
				t.Errorf("step gate = %+v, want reviewer-summary/grok-4.5", g)
			}
		}
	}
	if !found {
		t.Error("expected Gates row for step-summary node")
	}

	// role=agent on step-summary fails closed.
	agentsBad := agents
	agentsBad.Agents = map[string]config.AgentBinding{
		"reviewer-summary": {Command: "fake", Role: "agent", Tools: "Read", Model: "x"},
	}
	rBad := Validate(agentsBad, nil, wfNamed)
	if rBad.OK() {
		t.Fatal("role=agent on step-summary must fail")
	}

	// Missing binding fails closed.
	agentsMiss := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file", Model: "opus"},
	}
	rMiss := Validate(agentsMiss, nil, wfNamed)
	if rMiss.OK() {
		t.Fatal("missing binding on step-summary must fail")
	}

	// The plain agent=reviewer summariser stays green.
	wfLegacy := routeDocs(
		`["*"]
obligations = ["raised", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]

[[gate]]
skill = "satelle-step-summary"
agent = "reviewer"
mandatory = true
`)
	rLeg := Validate(agents, nil, wfLegacy)
	if !rLeg.OK() {
		t.Fatalf("legacy agent=reviewer step must pass: %v", rLeg.Problems)
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
			"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "opus"},
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
		Body: "",
	}}
	_ = wfs
	wfs = routeDocs(
		`["*"]
obligations = ["raised", "worked", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[worked]
status = "work"
agent = "ghost"
skills = ["code"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["worked"]
`)
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
		Body: "",
	}}
	_ = wfs
	wfs = routeDocs(
		`["*"]
obligations = ["raised", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]
`)
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

// on_enter_agent validation lived here (sty_5cabe26f): the entry performer had
// to resolve to a binding and counted as a use so the binding was not flagged
// orphaned. Flat dispatch retired entry dispatch entirely (sty_05a5e203), so
// there is no entry binding to validate — an ADVISOR is declared on the route,
// consulted by the orchestrator, and is not a dispatch target.
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

// TestValidate_CodexExecAndACP (sty_3b4909bb): command DefaultCodexExecCommand
// is ReadOnly for reviewers; danger sandbox hard-rejects; ACP spawn accepts.
func TestValidate_CodexExecAndACP(t *testing.T) {
	// Accept: DefaultCodexExecCommand + role=reviewer.
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{
			Command: agentcli.DefaultCodexExecCommand,
			Role:    "reviewer",
		},
	}
	r := Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("DefaultCodexExecCommand reviewer must pass: %v", r.Problems)
	}
	var g Grant
	for _, x := range r.Grants {
		if x.Name == "reviewer" {
			g = x
		}
	}
	if !g.ReadOnly {
		t.Error("codex exec -s read-only must count as ReadOnly ceiling")
	}
	if g.Backend != "isolated:codex" {
		t.Errorf("backend = %q, want isolated:codex", g.Backend)
	}

	// Hard-reject: danger-full-access for role=reviewer.
	agents.Reviewer = config.AgentBinding{
		Command: "codex exec -s danger-full-access {system}",
		Role:    "reviewer",
	}
	r = Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("danger-full-access reviewer must fail validate")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "danger-full-access") {
		t.Errorf("problems must name danger-full-access:\n%s", joined)
	}

	// Hard-reject: --dangerously-bypass-approvals-and-sandbox.
	agents.Reviewer = config.AgentBinding{
		Command: "codex exec --dangerously-bypass-approvals-and-sandbox {system}",
		Role:    "reviewer",
	}
	r = Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("dangerously-bypass reviewer must fail validate")
	}
	joined = strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "dangerously-bypass") {
		t.Errorf("problems must name dangerously-bypass:\n%s", joined)
	}

	// Accept: Codex ACP preferred path.
	agents.Reviewer = config.AgentBinding{
		Interface: "acp",
		Command:   agentcli.DefaultCodexACPCommand,
		Tools:     "read_file,grep,list_dir",
		Role:      "reviewer",
		Model:     "o4-mini",
	}
	r = Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("DefaultCodexACPCommand acp reviewer must pass: %v", r.Problems)
	}
	for _, x := range r.Grants {
		if x.Name == "reviewer" {
			g = x
		}
	}
	if g.Interface != "acp" || !strings.HasPrefix(g.Backend, "acp:") {
		t.Errorf("grant = %+v, want interface=acp backend acp:*", g)
	}
	if !g.ReadOnly {
		t.Error("acp codex reviewer with read-only tools should be ReadOnly")
	}
}

// TestValidate_CodexReviewerSandboxHardReject (sty_aa726901 AC3): workspace-write
// and omitted sandbox hard-reject for role=reviewer Codex command templates;
// Claude/Grok templates still pass; ACP Codex does not require -s.
func TestValidate_CodexReviewerSandboxHardReject(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
	}

	// workspace-write → Problem naming workspace-write.
	agents.Reviewer = config.AgentBinding{
		Command: "codex exec -s workspace-write -m {model} {system}",
		Role:    "reviewer",
	}
	r := Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("workspace-write Codex reviewer must fail validate")
	}
	joined := strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "workspace-write") {
		t.Errorf("problems must name workspace-write:\n%s", joined)
	}

	// Omitted sandbox → Problem.
	agents.Reviewer = config.AgentBinding{
		Command: "codex exec -m {model} {system}",
		Role:    "reviewer",
	}
	r = Validate(agents, nil, nil)
	if r.OK() {
		t.Fatal("omitted-sandbox Codex reviewer must fail validate")
	}
	joined = strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "none (omitted)") && !strings.Contains(joined, "read-only") {
		t.Errorf("problems must name omitted sandbox:\n%s", joined)
	}

	// Claude default template → pass (no Codex sandbox problem).
	agents.Reviewer = config.AgentBinding{
		Command: agentcli.DefaultClaudeCommand,
		Role:    "reviewer",
		Tools:   "Read,Grep,Glob",
	}
	r = Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("DefaultClaudeCommand reviewer must pass: %v", r.Problems)
	}

	// Grok default template → pass.
	agents.Reviewer = config.AgentBinding{
		Command: agentcli.DefaultGrokCommand,
		Role:    "reviewer",
		Tools:   "read_file,grep,list_dir",
	}
	r = Validate(agents, nil, nil)
	if !r.OK() {
		t.Fatalf("DefaultGrokCommand reviewer must pass: %v", r.Problems)
	}

	// Danger still uses danger message (not double sandbox message).
	agents.Reviewer = config.AgentBinding{
		Command: "codex exec -s danger-full-access {system}",
		Role:    "reviewer",
	}
	r = Validate(agents, nil, nil)
	joined = strings.Join(r.Problems, "\n")
	if !strings.Contains(joined, "danger-full-access") {
		t.Errorf("danger must keep danger message:\n%s", joined)
	}
	// Should not also require "want -s read-only" path for the same binding when danger fires first.
	if strings.Count(joined, "\n")+1 > 2 && strings.Contains(joined, "want -s read-only") && strings.Contains(joined, "disables the sandbox") {
		// both messages would be a bug; allow only danger path
		t.Errorf("danger and sandbox hard-reject must not both fire:\n%s", joined)
	}
}

// channelWF is a minimal spine with ONE named performer node, used to vary only
// the binding under test (sty_87c0ef37).
func channelWF(section string) []docindex.Doc {
	return routeDocs(
		`["*"]
obligations = ["raised", "planned", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "`+section+`"
skills = ["plan"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["planned"]
`)
}

// TestValidate_ContextChannelFindings is the AC1/AC2/AC7/AC8 matrix: a DISPATCHED
// performer must carry a pull-context channel (error when it does not), and a
// role=reviewer binding that grants shell holds capability it never uses
// (warning). Correct configurations produce neither. Nothing dispatches an agent
// — Validate is store-free and spawns no process.
func TestValidate_ContextChannelFindings(t *testing.T) {
	cases := []struct {
		name        string
		section     string // binding name allocated to the performing node
		binding     config.AgentBinding
		wantProblem bool
		wantWarning bool
	}{{
		name:        "claude-only Read is not a channel — matches the runtime refusal",
		section:     "planner",
		binding:     config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus"},
		wantProblem: true,
	}, {
		name:        "empty grant",
		section:     "planner",
		binding:     config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Model: "opus"},
		wantProblem: true,
	}, {
		name:    "satelle CLI channel",
		section: "planner",
		binding: config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "opus"},
	}, {
		name:    "disk-read channel",
		section: "planner",
		binding: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
	}, {
		name:    "broad shell channel",
		section: "planner",
		binding: config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "*", Model: "opus"},
	}, {
		name:    "in-loop performer is exempt — never dispatched",
		section: "planner",
		binding: config.AgentBinding{Command: "in-loop"},
	}, {
		// A repo-agnostic name (AC5): nothing keys off "planner".
		name:        "arbitrary binding name is reported, not a known one",
		section:     "architect",
		binding:     config.AgentBinding{Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus"},
		wantProblem: true,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agents := config.AgentsConfig{
				Executor: config.AgentBinding{Command: "in-loop"},
				Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
				Agents:   map[string]config.AgentBinding{tc.section: tc.binding},
			}
			wfs := channelWF(tc.section)
			r := Validate(agents, nil, wfs)

			got := findingWith(r.Problems, "no context channel")
			if tc.wantProblem && got == "" {
				t.Fatalf("want context-channel problem, got problems=%v", r.Problems)
			}
			if !tc.wantProblem && got != "" {
				t.Fatalf("unexpected context-channel problem: %s", got)
			}
			if tc.wantProblem {
				if !strings.Contains(got, tc.section) {
					t.Errorf("problem must name the binding %q: %s", tc.section, got)
				}
				if !strings.Contains(got, "Bash(satelle:*)") || !strings.Contains(got, "read_file") {
					t.Errorf("problem must name BOTH fixes: %s", got)
				}
				if !strings.Contains(got, `node "plan"`) {
					t.Errorf("problem must name the allocating node: %s", got)
				}
				if r.OK() {
					t.Error("a missing context channel must make the report not OK (non-zero exit)")
				}
			}
		})
	}
}

// TestValidate_ReviewerShellGrantIsUnusedCapability — AC2/AC4: reviewers are fed
// their documents in the transition payload, so a shell grant is never consulted.
// Reported as a WARNING (report stays OK → exit 0) because keeping it is the
// repo's call; it is a statement of fact, not a prohibition.
func TestValidate_ReviewerShellGrantIsUnusedCapability(t *testing.T) {
	mk := func(tools string) Report {
		agents := config.AgentsConfig{
			Executor: config.AgentBinding{Command: "in-loop"},
			Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: tools, Model: "grok-4.5", Role: "reviewer"},
			Agents: map[string]config.AgentBinding{
				"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "opus"},
			},
		}
		wfs := channelWF("planner")
		return Validate(agents, nil, wfs)
	}

	r := mk("read_file,Bash(satelle:*)")
	w := findingWith(r.Warnings, "grants shell")
	if w == "" {
		t.Fatalf("want unused-shell warning, got warnings=%v", r.Warnings)
	}
	if !strings.Contains(w, "reviewer") {
		t.Errorf("warning must name the binding section: %s", w)
	}
	if !strings.Contains(w, "Bash(satelle:*)") {
		t.Errorf("warning must name the offending token: %s", w)
	}
	if !r.OK() {
		t.Error("an unused reviewer grant is advisory — the report must stay OK (exit 0)")
	}
	if p := findingWith(r.Problems, "grants shell"); p != "" {
		t.Errorf("must not be a hard problem: %s", p)
	}

	// A reviewer with no shell grant is the correct shape — no finding (AC7).
	if w := findingWith(mk("read_file,grep,list_dir").Warnings, "grants shell"); w != "" {
		t.Errorf("clean reviewer must not warn: %s", w)
	}
}

// TestValidate_UnallocatedBindingHasNoChannelFinding — AC8: a binding no workflow
// allocates is an orphan, not a performer. Its grant is not judged for a channel
// it will never need.
func TestValidate_UnallocatedBindingHasNoChannelFinding(t *testing.T) {
	agents := config.AgentsConfig{
		Executor: config.AgentBinding{Command: "in-loop"},
		Reviewer: config.AgentBinding{Command: agentcli.DefaultGrokCommand, Tools: "read_file,grep,list_dir", Model: "grok-4.5"},
		Agents: map[string]config.AgentBinding{
			"planner": {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob,Bash(satelle:*)", Model: "opus"},
			"nobody":  {Command: agentcli.DefaultClaudeCommand, Tools: "Read,Grep,Glob", Model: "opus"},
		},
	}
	wfs := channelWF("planner")
	r := Validate(agents, nil, wfs)
	if p := findingWith(r.Problems, "no context channel"); p != "" {
		t.Fatalf("unallocated binding must not be judged as a performer: %s", p)
	}
	if !r.OK() {
		t.Fatalf("orphan alone must not fail the report: %v", r.Problems)
	}
}

func findingWith(list []string, substr string) string {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return s
		}
	}
	return ""
}
