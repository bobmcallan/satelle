package help

import (
	"strings"
	"testing"
)

func TestListContainsCoreTopics(t *testing.T) {
	names := map[string]bool{}
	for _, top := range List() {
		names[top.Name] = true
		if top.Title == "" {
			t.Errorf("topic %q has no title", top.Name)
		}
		if strings.TrimSpace(top.Body) == "" {
			t.Errorf("topic %q has empty body", top.Name)
		}
	}
	for _, want := range []string{"create-story", "reviewer-checks", "principles", "projects", "create-review", "agent-dispatch"} {
		if !names[want] {
			t.Errorf("missing help topic %q", want)
		}
	}
}

func TestAgentDispatchTopic(t *testing.T) {
	top, ok := Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch topic not found")
	}
	// The topic must teach the whole dispatch contract from deployed docs alone:
	// how the agent is briefed, how it PULLS context by id, the refusals, what
	// makes a step self-sufficient, and the entry-dispatch / exit-review rule.
	for _, want := range []string{
		"agents.toml",            // where the binding lives
		"@skill:",                // the rubric requirement
		"inject_principles",      // the principle-injection toggle
		"refuse",                 // fail-loud on a missing binding / grant
		"Bash(satelle:*)",        // the grant a dispatched agent needs
		"satelle story get <id>", // the pull commands (must match the shipped prompt)
		"satelle ledger list --story <id>",
		"self-sufficient",                 // the sufficiency precondition
		"entry",                           // dispatch fires on entry
		"EXIT edge",                       // judge the exit edge
		"{story, from, to, review_skill}", // the stdin shape (pull model, not push)
		"[architect]",                     // the custom-agent worked example (binding)
		"agent=architect",                 // the allocation
		".claude/agents",                  // the harness-agent-dir anti-pattern
		// Full-template requirement + placeholders (AC4, sty_6752e35b): bare
		// single-token presets are rejected; only in-loop remains as a bare token.
		// `satelle help agent-dispatch` teaches this without reading the source.
		`command = "in-loop"`,
		"full multi-token command template",
		"rejected",
		"{system}", "{tools}", "{model}", "{payload}",
		"deprecated alias", // harness→command rename is documented as back-compat
		// Dual transport (epic:agent-dispatch-transport): CLI control plane in;
		// command default + optional ACP out; Claude command-only; no MCP process API.
		`interface`,
		"command",
		"acp",
		"CLI verbs",
		"Claude",
		"MCP",
		"story status",
		"effort",    // sty_657f77b9
		"secondary", // sty_5bf61f89
		// Codex dual transport + dogfood (sty_3b4909bb / sty_aa726901).
		"DefaultCodexACPCommand",
		"codex exec",
		"@agentclientprotocol/codex-acp",
		"SATELLE_CODEX_DOGFOOD",
		"INITIAL_AGENT_MODE",
		"satelle agents install",
		"model_reasoning_effort",
		"compliance",        // sty_9e86f407
		".codex/hooks.json", // sty_9e86f407
		"story is engaged",  // sty_9e86f407
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("agent-dispatch topic missing %q", want)
		}
	}
}

// TestCreateReviewTopic asserts the worked example is complete enough to
// self-serve (sty_51ad783b): the full skill anatomy, the workflow binding, the
// opt-in framing, and how to confirm the wiring.
func TestCreateReviewTopic(t *testing.T) {
	top, ok := Get("create-review")
	if !ok {
		t.Fatal("create-review topic not found")
	}
	for _, want := range []string{
		"type: skill",                         // the rubric skill frontmatter
		`{"decision": "accept", "notes": ""}`, // the verdict contract
		"create_review: my-create-review",     // the workflow binding
		"gate_create = true",                  // the repo opt-in
		"workflow validate",                   // how a broken binding is surfaced
		"deterministic",                       // the degradation story (opt-in framing)
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("create-review topic missing %q", want)
		}
	}
}

// TestReviewerChecksTopic pins the restructured DOT-bullet content after the
// paste-defect repair (sty_46c584b1): validate sentence and done-gate note sit
// outside the fenced-DOT bullet, not jammed mid-bullet.
func TestReviewerChecksTopic(t *testing.T) {
	top, ok := Get("reviewer-checks")
	if !ok {
		t.Fatal("reviewer-checks topic not found")
	}
	for _, want := range []string{
		"satelle <noun> validate",
		"DETERMINISTIC",
		"The done gate is **not** mandated",
		"@skill:",
		"gated transition",
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("reviewer-checks topic missing %q", want)
		}
	}
}

// TestWorkflowsTopic pins the binding-form section (sty_9882b8c6).
func TestWorkflowsTopic(t *testing.T) {
	top, ok := Get("workflows")
	if !ok {
		t.Fatal("workflows topic not found")
	}
	for _, want := range []string{
		"Binding a reviewer: edge CSV vs scoped on=",
		"on= over-fire",
		"first-reject short-circuit",
		"list order = execution order",
		"Edge wins",
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("workflows topic missing %q", want)
		}
	}
}

func TestProjectsTopic(t *testing.T) {
	top, ok := Get("projects")
	if !ok {
		t.Fatal("projects topic not found")
	}
	// The topic must teach the key agent rule: add another project with
	// `workspace add`, served additively under /<slug>/.
	for _, want := range []string{"workspace add", "/<slug>/", "service install", "~/.satelle/config.toml"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("projects topic body missing %q", want)
		}
	}
}

func TestGet(t *testing.T) {
	top, ok := Get("create-story")
	if !ok {
		t.Fatal("create-story topic not found")
	}
	if !strings.Contains(top.Body, "acceptance criteria") {
		t.Errorf("create-story body missing expected content")
	}
	if _, ok := Get("does-not-exist"); ok {
		t.Error("expected miss for unknown topic")
	}
}
