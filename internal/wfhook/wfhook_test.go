package wfhook

import (
	"strings"
	"testing"
)

func wrap(fm string) string {
	return "---\n" + strings.TrimPrefix(fm, "\n") + "---\n\n# body\n"
}

// TestParseBothForms pins AC1 and AC2: the explicit hooks: block declares both
// skill and agent, and the pre-existing scalar shorthand resolves to the same
// Hook shape with a documented default agent and its own provenance — so no
// consumer branches on which form was written.
func TestParseBothForms(t *testing.T) {
	cases := []struct {
		name          string
		fm            string
		wantSkill     string
		wantAgent     string
		wantDeclared  bool
		wantSource    string
		wantVerdict   bool
		wantDescribes string
	}{
		{
			name:          "shorthand",
			fm:            "name: w\ncreate_review: satelle-story-create-review\n",
			wantSkill:     "satelle-story-create-review",
			wantAgent:     DefaultAgent,
			wantDeclared:  false,
			wantSource:    SourceShorthand,
			wantVerdict:   true,
			wantDescribes: "agent=reviewer (default, from create_review shorthand)",
		},
		{
			name:          "hooks block with an explicit agent",
			fm:            "name: w\nhooks:\n  - operation: create_review\n    skill: strict-create-review\n    agent: strict-reviewer\n",
			wantSkill:     "strict-create-review",
			wantAgent:     "strict-reviewer",
			wantDeclared:  true,
			wantSource:    SourceHooks,
			wantVerdict:   true,
			wantDescribes: "agent=strict-reviewer (declared in hooks)",
		},
		{
			name:          "hooks block omitting the agent defaults with provenance",
			fm:            "name: w\nhooks:\n  - operation: create_review\n    skill: strict-create-review\n",
			wantSkill:     "strict-create-review",
			wantAgent:     DefaultAgent,
			wantDeclared:  false,
			wantSource:    SourceHooks,
			wantVerdict:   true,
			wantDescribes: "agent=reviewer (default)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hooks, problems := Parse(wrap(c.fm))
			if len(problems) != 0 {
				t.Fatalf("unexpected problems: %v", problems)
			}
			if len(hooks) != 1 {
				t.Fatalf("want 1 hook, got %+v", hooks)
			}
			h := hooks[0]
			if h.Operation != OpCreateReview || h.Skill != c.wantSkill || h.Agent != c.wantAgent {
				t.Errorf("hook = %+v", h)
			}
			if h.AgentDeclared != c.wantDeclared || h.Source != c.wantSource || h.Verdict != c.wantVerdict {
				t.Errorf("provenance = %+v", h)
			}
			if got := h.Describe(); got != c.wantDescribes {
				t.Errorf("Describe = %q, want %q", got, c.wantDescribes)
			}
			if got, ok := For(wrap(c.fm), OpCreateReview); !ok || got != h {
				t.Errorf("For disagrees with Parse: %+v vs %+v", got, h)
			}
		})
	}
}

// TestParseDuplicateFormsPrefersTheExplicitEntry pins the ambiguity rule: a
// workflow declaring the same operation both ways resolves to the explicit
// entry, and the duplicate is reported rather than silently ignored.
func TestParseDuplicateFormsPrefersTheExplicitEntry(t *testing.T) {
	body := wrap("name: w\ncreate_review: shorthand-skill\nhooks:\n  - operation: create_review\n    skill: block-skill\n    agent: strict-reviewer\n")
	hooks, problems := Parse(body)
	if len(hooks) != 1 || hooks[0].Skill != "block-skill" || hooks[0].Agent != "strict-reviewer" {
		t.Fatalf("the explicit entry must win: %+v", hooks)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "both") {
		t.Errorf("the duplicate must be reported: %v", problems)
	}
}

// TestParseUnknownOperationIsCarriedNotDropped pins AC3's guard: an operation
// this binary does not know is kept as a Hook so validation can name it, and it
// never silently acquires verdict authority.
func TestParseUnknownOperationIsCarriedNotDropped(t *testing.T) {
	hooks, problems := Parse(wrap("name: w\nhooks:\n  - operation: close_review\n    skill: some-skill\n"))
	if len(hooks) != 1 || hooks[0].Operation != "close_review" || hooks[0].Skill != "some-skill" {
		t.Fatalf("an unknown operation must be carried: %+v", hooks)
	}
	if hooks[0].Verdict {
		t.Error("an unknown operation must not be treated as a verdict operation")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "close_review") {
		t.Errorf("the unknown operation must be reported by name: %v", problems)
	}
	if Known("close_review") || IsVerdict("close_review") {
		t.Error("close_review is not a known operation")
	}
}

// TestParseMalformedEntries pins the declaration problems a validator surfaces.
func TestParseMalformedEntries(t *testing.T) {
	cases := map[string]struct{ fm, want string }{
		"no operation": {"hooks:\n  - skill: s\n", "no operation"},
		"no skill":     {"hooks:\n  - operation: create_review\n", "no skill"},
		"duplicate op": {"hooks:\n  - operation: create_review\n    skill: a\n  - operation: create_review\n    skill: b\n", "more than once"},
		"unknown key":  {"hooks:\n  - operation: create_review\n    skill: a\n    model: opus\n", "unknown key"},
		"not a pair":   {"hooks:\n  - operation: create_review\n    skill: a\n    justwords\n", "not a key"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, problems := Parse(wrap("name: w\n" + c.fm))
			if len(problems) == 0 {
				t.Fatalf("want a problem for %s", name)
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("problems should mention %q: %v", c.want, problems)
			}
		})
	}
}

// TestParseCarriesNoExecutionConfiguration is AC3's structural line: a hook
// declares WHO runs the skill, never HOW. Keys that would pin a provider, model,
// or command are refused, so execution configuration cannot migrate out of
// agents.toml into workflow substrate through this door.
func TestParseCarriesNoExecutionConfiguration(t *testing.T) {
	for _, key := range []string{"model", "effort", "command", "interface", "tools", "timeout"} {
		_, problems := Parse(wrap("name: w\nhooks:\n  - operation: create_review\n    skill: s\n    " + key + ": x\n"))
		if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), key) {
			t.Errorf("%s must be refused on a hook: %v", key, problems)
		}
	}
	// The Hook type itself carries no execution field — asserted by construction:
	// every consumer resolves execution from the named agents.toml section.
	h := Hook{Operation: OpCreateReview, Skill: "s", Agent: "a"}
	if h.Describe() == "" {
		t.Error("Describe must render")
	}
}

// TestParseAbsentAndNonWorkflowBodies pins the degradation contract: a body with
// no frontmatter, or with no hook declaration, yields nothing and no problem —
// creation stays deterministic-only exactly as before.
func TestParseAbsentAndNonWorkflowBodies(t *testing.T) {
	for name, body := range map[string]string{
		"no frontmatter": "# just a doc\n",
		"unterminated":   "---\nname: w\n",
		"no hooks":       wrap("name: w\napplies_to: [\"*\"]\n"),
	} {
		hooks, problems := Parse(body)
		if len(hooks) != 0 || len(problems) != 0 {
			t.Errorf("%s: want nothing, got %+v / %v", name, hooks, problems)
		}
		if _, ok := For(body, OpCreateReview); ok {
			t.Errorf("%s: For must report undeclared", name)
		}
	}
}

// TestScalarIgnoresIndentedKeys guards the shorthand scan against picking up a
// nested key that merely shares the name (e.g. inside the hooks: block itself).
func TestScalarIgnoresIndentedKeys(t *testing.T) {
	hooks, problems := Parse(wrap("name: w\nhooks:\n  - operation: create_review\n    skill: block-skill\n"))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(hooks) != 1 || hooks[0].Source != SourceHooks {
		t.Fatalf("an indented operation key must not read as the shorthand: %+v", hooks)
	}
}

// TestOperationsTableIsTheOnlyPerOperationKnowledge pins AC3's boundary: the
// table names operations and their verdict nature — nothing else.
func TestOperationsTableIsTheOnlyPerOperationKnowledge(t *testing.T) {
	ops := Operations()
	want := []string{OpAmendReview, OpCreateReview} // sorted
	if len(ops) != len(want) {
		t.Fatalf("operations table = %v, want %v", ops, want)
	}
	for i, op := range want {
		if ops[i] != op {
			t.Fatalf("operations table = %v, want %v", ops, want)
		}
		if !IsVerdict(op) || !Known(op) {
			t.Errorf("%s is a known verdict operation", op)
		}
	}
}

// TestAmendReviewIsDeclarableInBothSpellings (sty_81aa4d8f): the amend gate is a
// lifecycle hook like create_review — declarable as the shorthand or as a hooks:
// entry with its own agent, and a verdict operation, so it carries a gate's
// requirements through validation.
func TestAmendReviewIsDeclarableInBothSpellings(t *testing.T) {
	short, problems := For(wrap("name: w\namend_review: satelle-story-amend-review\n"), OpAmendReview)
	if !problems || short.Skill != "satelle-story-amend-review" || short.Agent != DefaultAgent {
		t.Fatalf("shorthand hook = %+v (declared %v)", short, problems)
	}
	if !short.Verdict || short.Source != SourceShorthand {
		t.Errorf("amend_review must be a verdict operation with shorthand provenance: %+v", short)
	}
	block, declared := For(wrap("name: w\nhooks:\n  - operation: amend_review\n    skill: strict-amend\n    agent: strict-reviewer\n"), OpAmendReview)
	if !declared || block.Skill != "strict-amend" || block.Agent != "strict-reviewer" || !block.AgentDeclared {
		t.Fatalf("hooks-block hook = %+v (declared %v)", block, declared)
	}
	// Both spellings coexist with create_review rather than displacing it.
	hooks, probs := Parse(wrap("name: w\ncreate_review: c\namend_review: a\n"))
	if len(probs) != 0 || len(hooks) != 2 {
		t.Fatalf("hooks = %+v, problems = %v", hooks, probs)
	}
}
