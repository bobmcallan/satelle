package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfhook"
)

// hookDocs wraps a workflow body as the doc list WorkflowConsistency consumes.
func hookDocs(body string) []docindex.Doc {
	return []docindex.Doc{{Kind: "workflows", Name: "w", Body: body}}
}

// hookWF declares create review through the explicit `hooks:` form, allocating
// it to a NAMED reviewer binding — the allocation the old empty-selector path
// could not express.
var hookWF = func() string {
	// A lifecycle hook is workflow FRONTMATTER, and a derived route declares it on
	// its declaration of done (sty_9835070d).
	base := spineWF("", "", "", "in_progress|executor", "done")
	return strings.Replace(base, "scope: system\n---",
		"scope: system\nhooks:\n  - operation: create_review\n    skill: my-create-review\n    agent: strict-reviewer\n---", 1)
}()

// namedReviewer is an isolated read-only reviewer binding a hook can allocate.
func namedReviewer(model string) config.AgentBinding {
	return config.AgentBinding{
		Role:    config.RoleReviewer,
		Command: agentcli.DefaultClaudeCommand,
		Tools:   "Read,Grep,Glob",
		Model:   model,
	}
}

// TestReviewCreateUsesTheDeclaredAgent is AC1's proof: a workflow declaring an
// `agent:` on its create-review hook runs that binding, not the invisible
// `[reviewer]` fallback the empty selector used to resolve to. The named binding
// runs its OWN harness, so the bootstrap runner must not be the one invoked.
func TestReviewCreateUsesTheDeclaredAgent(t *testing.T) {
	g, bootstrap := newEngine(t, `{"decision":"accept"}`,
		fakeDocs{workflow: hookWF, skillBody: "content rubric", skillFound: true})

	var asked []string
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		asked = append(asked, name)
		if name == "strict-reviewer" {
			return namedReviewer("opus"), true
		}
		return config.AgentBinding{}, false
	})

	// The named binding builds its own runner from the command template, so the
	// call fails at exec (no such binary in the test env) rather than silently
	// falling back. Either outcome proves the point we are asserting: the engine
	// asked for the DECLARED section.
	_, _ = g.ReviewCreate(context.Background(), validDraft)

	if len(asked) == 0 {
		t.Fatal("the declared agent was never resolved — create review still uses the empty selector")
	}
	for _, name := range asked {
		if name != "strict-reviewer" {
			t.Errorf("resolved %q, want the declared strict-reviewer", name)
		}
	}
	if bootstrap.got.SystemPrompt != "" {
		t.Error("a named hook binding must run its own harness, not the bootstrap runner")
	}
}

// TestReviewCreateShorthandStillUsesTheDefaultReviewer pins AC2 at the engine:
// the scalar form behaves exactly as before — the bootstrap `[reviewer]` runner
// judges the draft — but now because the hook DECLARED that default, not because
// an empty string fell through gateBinding.
func TestReviewCreateShorthandStillUsesTheDefaultReviewer(t *testing.T) {
	g, r := newEngine(t, `{"decision":"accept","notes":"aligned"}`,
		fakeDocs{workflow: createWF, skillBody: "content rubric", skillFound: true})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		t.Error("the shorthand must not resolve a named binding")
		return config.AgentBinding{}, false
	})

	dec, err := g.ReviewCreate(context.Background(), validDraft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != "my-create-review" {
		t.Fatalf("want gated accept by the declared skill, got %+v", dec)
	}
	if r.got.SystemPrompt == "" {
		t.Error("the default reviewer's runner must have judged the draft")
	}

	// And the declaration itself is inspectable, with its default attributed.
	hook, ok := wfhook.For(createWF, wfhook.OpCreateReview)
	if !ok || hook.Agent != wfhook.DefaultAgent || hook.AgentDeclared {
		t.Errorf("shorthand hook = %+v", hook)
	}
}

// TestReviewCreateUndeclaredHookStaysDeterministic pins the degradation
// contract through the new path: a workflow declaring no hook keeps creation
// structure-only, with no agent invoked.
func TestReviewCreateUndeclaredHookStaysDeterministic(t *testing.T) {
	g, r := newEngine(t, `{"decision":"reject","notes":"should not run"}`,
		fakeDocs{workflow: plainWF, skillFound: false})
	dec, err := g.ReviewCreate(context.Background(), validDraft)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Gated || !dec.Accept || dec.Skill != structureSkill {
		t.Fatalf("no hook → deterministic accept, got %+v", dec)
	}
	if r.got.SystemPrompt != "" {
		t.Error("no content reviewer should run when the workflow declares none")
	}
}

// TestWorkflowConsistencyReportsHookDefects pins AC5's declaration half: an
// unresolved hook skill and a malformed/unknown declaration are both surfaced by
// the consistency check `satelle workflow validate` and `agent validate` run.
func TestWorkflowConsistencyReportsHookDefects(t *testing.T) {
	cases := map[string]struct {
		body    string
		resolve func(string) bool
		want    string
	}{
		"unresolved hook skill": {
			body:    hookWF,
			resolve: func(string) bool { return false },
			want:    "does not resolve in the substrate",
		},
		"unresolved shorthand skill": {
			body:    createWF,
			resolve: func(string) bool { return false },
			want:    "create_review",
		},
		"unknown operation": {
			body:    "---\nname: w\napplies_to: [\"*\"]\nhooks:\n  - operation: close_review\n    skill: s\n---\n",
			resolve: func(string) bool { return true },
			want:    "unknown operation",
		},
		"hook with no skill": {
			body:    "---\nname: w\napplies_to: [\"*\"]\nhooks:\n  - operation: create_review\n---\n",
			resolve: func(string) bool { return true },
			want:    "declares no skill",
		},
		"declared both ways": {
			body:    "---\nname: w\napplies_to: [\"*\"]\ncreate_review: a\nhooks:\n  - operation: create_review\n    skill: b\n---\n",
			resolve: func(string) bool { return true },
			want:    "both",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			problems := WorkflowConsistency(hookDocs(c.body), c.resolve)
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("want a problem mentioning %q, got %v", c.want, problems)
			}
			if !strings.Contains(joined, "workflow ") {
				t.Errorf("the problem must name the workflow: %v", problems)
			}
		})
	}
}

// TestWorkflowConsistencyAcceptsAHealthyHook guards against the checks above
// firing on a well-formed declaration.
func TestWorkflowConsistencyAcceptsAHealthyHook(t *testing.T) {
	problems := WorkflowConsistency(hookDocs(hookWF), func(string) bool { return true })
	for _, p := range problems {
		if strings.Contains(p, "create_review") || strings.Contains(p, "hooks:") {
			t.Errorf("a healthy hook must produce no problem: %v", problems)
		}
	}
}
