package help

import (
	"regexp"
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
		"story proof",
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
	for _, want := range []string{"done.toml", "step.toml", "applies_to"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not mention %q", want)
		}
	}
	// Every route-grammar key an agent has to write. A key missing here is a key
	// the agent has to guess.
	for _, key := range []string{
		"status", "requires", "reviewers", "reviewer_agent", "parallel",
		"terminal", "start", "park", "cancel", "recover", "[[gate]]", "for", "mandatory",
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
	for _, want := range []string{
		"satelle workflow validate", "satelle workflow show", "satelle story route", "satelle migrate",
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not name the verification step %q", want)
		}
	}
}

// TestWorkflowConvertTopicCoversTheWild guards the gap sty_e184768e measured: a
// survey of every authored DOT graph on a real fleet found 15 constructs in use
// and this topic documented 8. An undocumented construct is one an agent either
// transcribes into a key the grammar rejects, or — worse — drops in silence.
func TestWorkflowConvertTopicCoversTheWild(t *testing.T) {
	top, ok := Get("workflow-convert")
	if !ok {
		t.Fatal("the conversion guide must ship: every refusal names it")
	}
	// Every construct the survey found. The eight already covered are asserted
	// by TestWorkflowConvertTopic; these are the seven that were not.
	for _, construct := range []string{
		"reviewer_skill", "rankdir", "on_enter_agent", "on_enter_prompt",
		"goal=", "vars=", "guardrails",
	} {
		if !strings.Contains(top.Body, construct) {
			t.Errorf("no target documented for %q, which is present in the wild", construct)
		}
	}
	// The dangerous one: a RETIRED mechanism, not a renamed key. An agent must
	// be told to re-home the advisor, and told that nothing dispatches it.
	for _, want := range []string{"RETIRED", "retired, not renamed", "advise = {", "ORCHESTRATOR"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the on_enter callout is missing %q", want)
		}
	}
	// The quiet one: real operator intent with no home in the grammar, and no
	// warning when it vanishes. Both halves must be said.
	for _, want := range []string{"constitution", "nothing will warn you"} {
		if !strings.Contains(strings.ToLower(top.Body), strings.ToLower(want)) {
			t.Errorf("the goal/vars/guardrails callout is missing %q", want)
		}
	}
	// The two per-repo DECISIONS — named as decisions, with the traps that make
	// them decisions rather than lookups.
	for _, want := range []string{
		"The two decisions only you can make",
		"WHOLLY", "[execution]", "[task]", "[substrate]", "ungated",
	} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the decisions section is missing %q", want)
		}
	}
	// The verify loop must be an instruction with an ORDER: migrate deletes the
	// graph you diff against.
	for _, want := range []string{"gate by gate", "before** `satelle migrate --yes`"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the verification loop is missing %q", want)
		}
	}
}

// TestWorkflowConvertTopicCoversTheMarkdownRouteSource (sty_81bb0dde AC5): the
// route source became TOML, and a repo already on the MARKDOWN route source is
// refused by name until it converts. That refusal points here — this topic, not
// a second one — so the key-by-key mapping has to be in it. Every construct the
// markdown spelling had needs a target, or an agent either transcribes a key the
// decoder rejects or drops it in silence.
func TestWorkflowConvertTopicCoversTheMarkdownRouteSource(t *testing.T) {
	top, ok := Get("workflow-convert")
	if !ok {
		t.Fatal("the conversion guide must ship")
	}
	if !strings.Contains(top.Body, "md-to-toml") {
		t.Fatal("the guide carries no md-to-toml mapping — the refusal names this page")
	}
	for _, construct := range []string{
		"`---` frontmatter block", "`[meta]` table",
		"`## <category>` in done.md", "`- <obligation>` list",
		"`park: <state> @<gate>`", "`cancel: <state> @<gate>`", "`recover: <step> from a, b`",
		"`+ <tag> <obligation>`", "tag_obligation",
		"`## <name>` in step.md", "`provides: <obligation>`",
		"`## gate <skill>`", "`<!-- comment -->`",
		"a `hooks:` block list", "[[meta.hooks]]",
	} {
		if !strings.Contains(top.Body, construct) {
			t.Errorf("no md-to-toml target documented for %q", construct)
		}
	}
	// The ONE part that is not a transcription: markdown keyed a step by its
	// heading and named the obligation in `provides:`; TOML keys it by the
	// obligation. An agent that misses this writes a catalogue that collides.
	for _, want := range []string{"the KEY is what the step `provides:`", "stage names repeat"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the step-key rule is missing %q", want)
		}
	}
	// And the prose the new format makes unnecessary must be named as deletable,
	// or every converted repo carries tuition for a format it no longer uses.
	for _, want := range []string{"HOW TO READ THIS FILE", "Delete them"} {
		if !strings.Contains(top.Body, want) {
			t.Errorf("the guide does not say to drop the retired preamble: missing %q", want)
		}
	}
}

// TestWorkflowConvertTopicIsRepoAgnostic: the topic ships in the binary and is
// read by every repo. A story id, a this-repo category or an example lifted
// from one repo's graph reads as instructions to everyone else.
//
// Stricter than TestEmbeddedHelpHasNoUnreachableReferences (which permits bare
// (sty_…) provenance annotations elsewhere): workflow-convert must stay fully
// generic. Kept as its own test so that guarantee cannot be weakened by folding
// into the corpus-wide rule (sty_a319db89 architecture note).
func TestWorkflowConvertTopicIsRepoAgnostic(t *testing.T) {
	top, ok := Get("workflow-convert")
	if !ok {
		t.Fatal("the conversion guide must ship")
	}
	if strings.Contains(top.Body, "sty_") {
		t.Error("the topic names a story id — it ships to repos that have never seen it")
	}
	// A named graph file is an example nobody else has on disk — including the
	// embedded defaults, which no longer ship. Describe the graph by what it
	// DECLARES (`applies_to: [...]`), never by its filename.
	if strings.Contains(top.Body, "-workflow.md") {
		t.Error("the topic names a specific graph file — describe graphs by what they declare")
	}
	// A path under .satelle/skills or a bare skill name is this repo's
	// substrate; the topic's own examples must stay generic.
	if strings.Contains(top.Body, ".satelle/skills/") {
		t.Error("the topic reaches into a repo's authored skills")
	}
}

// TestEmbeddedHelpHasNoUnreachableReferences sweeps every shipped help topic
// for unreachable/repo-local authority (sty_a319db89 AC2 help half).
//
// Scope is help topics ONLY. Embedded substrate is owned by
// internal/config/substrate_conformance_test.go (dogfoodIDRe, dogfood-repo
// deferral, gitignored-path deferral) — do not re-scan that corpus from here.
//
// Bare "(sty_xxxxxxxx)" provenance annotations are allowed: agent-dispatch and
// others use them as citations, not as "see sty_… for the rule". Authority
// deferral (see/per/reason/decision … sty_) is banned.
func TestEmbeddedHelpHasNoUnreachableReferences(t *testing.T) {
	dogfoodRepo := regexp.MustCompile(`(?i)dogfood[ -]?repo`)
	// Authority deferral to a story id (not bare provenance parentheticals like
	// "… (sty_xxxxxxxx)." at end of a sentence, and not "per-binding").
	// Word-bounded verbs only — "per-binding" must not match \bper\b.
	styAuthority := regexp.MustCompile(`(?i)(\bsee\b|\bconsult\b|\brationale\b|full reason|\bdecision\b).{0,40}sty_[0-9a-f]{8}|\bper\s+sty_[0-9a-f]{8}`)
	// Deferral to a gitignored tree path as something the reader should open.
	gitignoredDeferral := regexp.MustCompile(`(?i)(see|consult|read|open|record in|decision).{0,60}\.satelle/(documents|stories)/`)
	for _, top := range List() {
		for i, line := range strings.Split(top.Body, "\n") {
			ln := i + 1
			if m := dogfoodRepo.FindString(line); m != "" {
				t.Errorf("%s:%d: dogfood-repo reference in shipped help (%q)", top.Name, ln, m)
			}
			if m := styAuthority.FindString(line); m != "" {
				t.Errorf("%s:%d: story id cited as authority for a product rule (%q)", top.Name, ln, m)
			}
			if m := gitignoredDeferral.FindString(line); m != "" {
				t.Errorf("%s:%d: defers to a gitignored path (%q)", top.Name, ln, m)
			}
		}
	}
}
