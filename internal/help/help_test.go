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
	for _, want := range []string{"create-story", "reviewer-checks", "principles", "projects", "create-review", "agent-dispatch", "workflow-convert"} {
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
		"skills:",                // the rubric requirement
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
		"agent: architect",                // the allocation
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
		"codex login", // sty_71491143: agent CLI owns auth, not satelle
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

// TestReviewerChecksTopic pins the lifecycle section: the authored form is a
// DERIVED ROUTE (sty_d953c5d8), the validate sentence and the done-gate note sit
// outside it, and the gates a step declares are named where they belong.
func TestReviewerChecksTopic(t *testing.T) {
	top, ok := Get("reviewer-checks")
	if !ok {
		t.Fatal("reviewer-checks topic not found")
	}
	for _, want := range []string{
		"satelle <noun> validate",
		"DETERMINISTIC",
		"The done gate is **not** mandated",
		"derived route",
		"gating ENTRY to it",
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("reviewer-checks topic missing %q", want)
		}
	}
}

// TestWorkflowsTopic pins the binding-form section (sty_9882b8c6), restated for
// the route grammar: a gate belongs to the step it admits, and an always-on
// `## gate` is the multi-step form (sty_d953c5d8).
func TestWorkflowsTopic(t *testing.T) {
	top, ok := Get("workflows")
	if !ok {
		t.Fatal("workflows topic not found")
	}
	for _, want := range []string{
		"Binding a reviewer: a step's `reviewers:` vs an always-on `## gate`",
		"The over-fire trap",
		"first-reject short-circuit",
		"List order = execution order",
		"Concurrency is the default",
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
	// An operator who sees a stale UI must find the recovery here rather than
	// having to know that `workspace add` happens to re-seed (sty_e6e467fe).
	for _, want := range []string{"stale", "re-request", "satelle workspace add"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("projects topic must document the stale-mirror recovery: missing %q", want)
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

// TestWorkflowConvertTopic: when the DOT front end retired, a repo that had not
// converted started REFUSING transitions, and the refusal points an agent here
// (sty_d953c5d8). This topic is therefore the only thing standing between a
// broken repo and a stuck agent, so it must actually carry the mapping — not
// just say that a conversion is owed.
func TestWorkflowConvertTopic(t *testing.T) {
	top, ok := Get("workflow-convert")
	if !ok {
		t.Fatal("the conversion guide must ship: every refusal names it")
	}
	// The two files, and the frontmatter rule that trips every first attempt.
	for _, want := range []string{"done.md", "step.md", "applies_to"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not mention %q", want)
		}
	}
	// Every route-grammar key an agent has to write. A key missing here is a key
	// the agent has to guess.
	for _, key := range []string{
		"provides", "requires", "reviewers", "reviewer_agent", "parallel",
		"terminal", "start", "park:", "cancel:", "recover:", "## gate", "for:", "mandatory",
	} {
		if !strings.Contains(top.Body, key) {
			t.Errorf("the guide does not cover the %q key", key)
		}
	}
	// The two mistakes the conversion actually makes: authoring the topology the
	// binary owns, and forgetting that a category-specific workflow is a section.
	for _, want := range []string{"Do not author topology", "SECTION"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not warn about %q", want)
		}
	}
	// And how to prove the conversion kept every gate.
	for _, want := range []string{"satelle workflow validate", "satelle story route", "satelle migrate"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not name the verification step %q", want)
		}
	}
}
