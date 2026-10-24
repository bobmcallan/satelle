package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A task declares typed inputs and an execution carries values for them; the
// performer and both validators receive them in the payload (story.inputs) and
// as SATELLE_INPUT_<NAME> variables.

func runWithInputs() workitem.Item {
	return workitem.Item{
		ID: "exe_1", Kind: workitem.KindExecution, Status: "in_progress",
		InputValues: map[string]any{"story_id": "sty_1", "retries": int64(3)},
	}
}

// payloadInputsOf decodes the story.inputs a payload carries.
func payloadInputsOf(t *testing.T, payload string) map[string]any {
	t.Helper()
	var p struct {
		Story struct {
			Inputs map[string]any `json:"inputs"`
		} `json:"story"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, payload)
	}
	return p.Story.Inputs
}

func assertRunInputs(t *testing.T, env map[string]string, payload string) {
	t.Helper()
	if env["SATELLE_INPUT_STORY_ID"] != "sty_1" || env["SATELLE_INPUT_RETRIES"] != "3" {
		t.Errorf("env lacks the inputs: %v", env)
	}
	in := payloadInputsOf(t, payload)
	if in["story_id"] != "sty_1" || in["retries"] != float64(3) {
		t.Errorf("payload story.inputs = %v", in)
	}
}

// The before-validate gate is a functional check: it never reaches Invoke, so
// the check's own environment and stdin carry the inputs.
func TestFunctionalCheckGateReceivesInputs(t *testing.T) {
	g, _ := newEngine(t, `{"decision":"reject"}`, fakeDocs{workflow: testWorkflow, skillBody: capCheckSkill, skillFound: true})
	var gotCtx context.Context
	var stdin string
	g.check = func(ctx context.Context, _, _, payload string) (string, error) {
		gotCtx, stdin = ctx, payload
		return "ok\n", nil
	}
	if _, err := g.Gate(context.Background(), runWithInputs(), "done"); err != nil {
		t.Fatal(err)
	}
	if gotCtx == nil {
		t.Fatal("the check did not run")
	}
	assertRunInputs(t, gateInputEnvFrom(gotCtx), stdin)

	// execCheck turns a marked context into the child's real environment (the
	// gate's own context is cancelled once the check returns).
	out, err := execCheck(withGateItem(context.Background(), runWithInputs()), t.TempDir(), `printenv SATELLE_INPUT_STORY_ID SATELLE_INPUT_RETRIES; cat`, stdin)
	if err != nil {
		t.Fatalf("execCheck: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "sty_1\n3\n") || !strings.Contains(out, `"inputs"`) {
		t.Errorf("check output:\n%s", out)
	}
}

// A `when` precondition is a functional check too.
func TestWhenPreconditionReceivesInputs(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{workflow: testWorkflow, skillBody: capCheckSkill, skillFound: true})
	var gotCtx context.Context
	var stdin string
	g.check = func(ctx context.Context, _, _, payload string) (string, error) {
		gotCtx, stdin = ctx, payload
		return "", nil
	}
	if !g.whenAllows(context.Background(), runWithInputs(), "done", wfdot.ScopedReviewer{Skill: "gate", When: "cond"}) {
		t.Fatal("exit 0 must run the gate")
	}
	assertRunInputs(t, gateInputEnvFrom(gotCtx), stdin)
}

// An agent-dispatched validator (the after-validate gate) gets the inputs through
// Invoke's environment and its transition payload.
func TestInvokedValidatorReceivesInputs(t *testing.T) {
	fr := &fakeRunner{out: `{"decision":"accept"}`}
	g := New(fr, fakeDocs{}, t.TempDir(), "")
	item := runWithInputs()
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Tools: "Read", Principles: config.PrinciplesNone},
		Section: "reviewer", Rubric: "judge", StoryID: item.ID, Skill: "test-skill",
		Payload: transitionPayload{Story: item, From: "in_progress", To: "done"},
		Expect:  ExpectVerdict, Runner: fr,
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	assertRunInputs(t, fr.got.Env, fr.got.Payload)
	if fr.got.Env[config.DispatchItemEnv] != item.ID {
		t.Errorf("the dispatch markers must still be set: %v", fr.got.Env)
	}
}

// A headless performer reaches Invoke with the same payload, so it gets the same.
func TestHeadlessPerformerReceivesInputs(t *testing.T) {
	g, fr := executorEngine(t, "fake -p {system}", "ok")
	if _, err := g.PerformExecutorStep(context.Background(), runWithInputs(), "exec", ""); err != nil {
		t.Fatal(err)
	}
	assertRunInputs(t, fr.got.Env, fr.got.Payload)
}

// An in-loop performer is already running, so the inputs ride its rubric: the
// JSON and the export lines — with or without a skill on the step.
func TestInLoopPerformerReceivesInputs(t *testing.T) {
	item := runWithInputs()
	item.InputValues["note"] = "it's, quoted"
	for name, steps := range map[string][]string{
		"with a skill": {"inloop|operator|skill-b", "done"},
		"no skill":     {"inloop|operator|", "done"},
	} {
		t.Run(name, func(t *testing.T) {
			g := engineFor(t, fakeDocs{workflow: spineWF("", "", "", steps...), extraSkills: twoSkills()}, nil, &fakeRunner{out: "ok"})
			res, err := g.DispatchExecutor(context.Background(), item, "inloop")
			if err != nil {
				t.Fatal(err)
			}
			if res.Dispatched || res.Kind != verb.PerformerInLoop {
				t.Fatalf("Dispatched=%v Kind=%q", res.Dispatched, res.Kind)
			}
			for _, want := range []string{
				"## Inputs", `"retries":3`, `"story_id":"sty_1"`,
				"export SATELLE_INPUT_STORY_ID='sty_1'", "export SATELLE_INPUT_RETRIES='3'",
				`export SATELLE_INPUT_NOTE='it'\''s, quoted'`,
			} {
				if !strings.Contains(res.InLoopRubric, want) {
					t.Errorf("rubric lacks %q:\n%s", want, res.InLoopRubric)
				}
			}
			if name == "with a skill" && !strings.Contains(res.InLoopRubric, "rubric B") {
				t.Errorf("the skill rubric must still lead:\n%s", res.InLoopRubric)
			}
		})
	}
}

// An execution of a task that declares no inputs behaves exactly as before.
func TestNoInputsChangesNothing(t *testing.T) {
	item := workitem.Item{ID: "exe_2", Kind: workitem.KindExecution, Status: "in_progress"}

	b, _ := json.Marshal(transitionPayload{Story: item, From: "in_progress", To: "done"})
	if strings.Contains(string(b), "inputs") {
		t.Errorf("an input-less payload grew an inputs key: %s", b)
	}
	if env := gateInputEnvFrom(withGateItem(context.Background(), item)); len(env) != 0 {
		t.Errorf("an input-less check carries env: %v", env)
	}
	if got := inLoopInputs(item); got != "" {
		t.Errorf("an input-less item has a rubric block: %q", got)
	}

	g := engineFor(t, fakeDocs{workflow: spineWF("", "", "", "inloop|operator|", "done"), extraSkills: twoSkills()}, nil, &fakeRunner{out: "ok"})
	res, err := g.DispatchExecutor(context.Background(), item, "inloop")
	if err != nil || res.InLoopRubric != "" || len(res.Skills) != 0 {
		t.Errorf("a no-skill, no-input step must deliver nothing: %+v, %v", res, err)
	}

	fr := &fakeRunner{out: `{"decision":"accept"}`}
	g = New(fr, fakeDocs{}, t.TempDir(), "")
	g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Tools: "Read", Principles: config.PrinciplesNone},
		Section: "reviewer", Rubric: "judge", StoryID: item.ID, Skill: "test-skill",
		Payload: transitionPayload{Story: item, From: "in_progress", To: "done"},
		Expect:  ExpectVerdict, Runner: fr,
	})
	for k := range fr.got.Env {
		if strings.HasPrefix(k, workitem.InputEnvPrefix) {
			t.Errorf("an input-less dispatch carries %s", k)
		}
	}
}
