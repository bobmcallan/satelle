package agentstep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// executorEngine is an engine over a route with one executor-allocated step
// ("exec", skill "rubric-skill") and one agent-less one, whose [executor] binding
// is the given command and whose runner is the fake it returns.
func executorEngine(t *testing.T, command, out string) (*Engine, *fakeRunner) {
	t.Helper()
	wf := spineWF("", "", "", "exec|executor|rubric-skill", "plain||", "done")
	g, _ := newEngine(t, "ok", fakeDocs{workflow: wf, skillBody: "EXECUTOR-RUBRIC", skillFound: true})
	fr := &fakeRunner{out: out}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return fr, nil }
	g.SetOneShotAgents(func(name string) (config.AgentBinding, bool) {
		if name != "executor" {
			return config.AgentBinding{}, false
		}
		return config.AgentBinding{Command: command, Tools: "Read,Bash(satelle:*)"}, true
	})
	return g, fr
}

var executorItem = workitem.Item{ID: "sty_1", Status: "backlog"}

// The [executor] binding performs a step the route allocates to it, with the
// step's executor skill as its rubric and a rejected edge's findings appended.
func TestPerformExecutorStepRunsTheBindingWithTheSkillAndTheFindings(t *testing.T) {
	g, fr := executorEngine(t, "fake -p {system}", "ok")
	res, err := g.PerformExecutorStep(context.Background(), executorItem, "exec", "FINDING-ONE: add the test")
	if err != nil {
		t.Fatalf("PerformExecutorStep: %v", err)
	}
	if !res.Dispatched || res.Agent != "executor" || res.Skill != "rubric-skill" {
		t.Errorf("result = %+v, want the executor binding performing under rubric-skill", res)
	}
	for _, want := range []string{"EXECUTOR-RUBRIC", "FINDING-ONE: add the test", `performing step "exec"`} {
		if !strings.Contains(fr.got.SystemPrompt, want) {
			t.Errorf("the system prompt must carry %q:\n%s", want, fr.got.SystemPrompt)
		}
	}
	if !strings.Contains(fr.got.Payload, "sty_1") {
		t.Errorf("the story payload must reach the binding:\n%s", fr.got.Payload)
	}

	// No findings: nothing is appended.
	g2, fr2 := executorEngine(t, "fake -p {system}", "ok")
	if _, err := g2.PerformExecutorStep(context.Background(), executorItem, "exec", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fr2.got.SystemPrompt, "Findings from the last presentation") {
		t.Errorf("a first run carries no findings section:\n%s", fr2.got.SystemPrompt)
	}
}

// sty_4470bb38: when the payload already carries the rejected exit's findings as
// findings_to_answer, the notes are not appended to the rubric a second time.
func TestPerformExecutorStepDeliversExitFindingsOnce(t *testing.T) {
	g, fr := executorEngine(t, "fake -p {system}", "ok")
	g.SetPriorVerdictsResolver(edgeResolver("exec", "plain",
		PriorVerdict{Skill: "exit-review", Decision: "reject", Notes: "EXIT-FINDING: the slice has no test"}))
	g.SetExitRejectResolver(exitRejectOf("exec", "plain"))
	item := workitem.Item{ID: "sty_1", Status: "exec"}
	if _, err := g.PerformExecutorStep(context.Background(), item, "exec", "EXIT-FINDING: the slice has no test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.got.Payload, "findings_to_answer") || !strings.Contains(fr.got.Payload, "exit-review#1") {
		t.Errorf("the exit's findings must ride the payload:\n%s", fr.got.Payload)
	}
	if strings.Contains(fr.got.SystemPrompt, "Findings from the last presentation") {
		t.Errorf("findings already in findings_to_answer must not be appended to the rubric:\n%s", fr.got.SystemPrompt)
	}
}

// An in-loop [executor] is never a quiet no-op, and a step not allocated to the
// executor is not this method's to perform.
func TestPerformExecutorStepRefusesWhatItCannotPerform(t *testing.T) {
	g, fr := executorEngine(t, "in-loop", "ok")
	if _, err := g.PerformExecutorStep(context.Background(), executorItem, "exec", ""); err == nil || !strings.Contains(err.Error(), "in-loop") {
		t.Errorf("an in-loop executor must be refused, got %v", err)
	}
	g, fr = executorEngine(t, "fake -p {system}", "ok")
	if _, err := g.PerformExecutorStep(context.Background(), executorItem, "plain", ""); err == nil || !strings.Contains(err.Error(), "not allocated to agent=executor") {
		t.Errorf("a step not allocated to the executor must be refused, got %v", err)
	}
	if fr.got.SystemPrompt != "" {
		t.Errorf("a refused step must not start the binding")
	}
	g, _ = executorEngine(t, "fake -p {system}", "ok")
	g.oneShotAgents = nil
	if _, err := g.PerformExecutorStep(context.Background(), executorItem, "exec", ""); err == nil {
		t.Errorf("an engine with no one-shot agents layer must refuse")
	}
}

// A performer that says reject comes back as a PerformerReject carrying its notes.
func TestPerformExecutorStepReportsAReject(t *testing.T) {
	g, _ := executorEngine(t, "fake -p {system}", `{"decision":"reject","notes":"the merge conflicts"}`)
	_, err := g.PerformExecutorStep(context.Background(), executorItem, "exec", "")
	var rej *verb.PerformerReject
	if !errors.As(err, &rej) || !strings.Contains(rej.Notes, "the merge conflicts") {
		t.Fatalf("want a PerformerReject with the notes, got %v", err)
	}
}

// A binding that fails is an error naming the step, not a quiet accept.
func TestPerformExecutorStepSurfacesAFailedRun(t *testing.T) {
	g, fr := executorEngine(t, "fake -p {system}", "")
	fr.err = errors.New("exit status 2")
	if _, err := g.PerformExecutorStep(context.Background(), executorItem, "exec", ""); err == nil || !strings.Contains(err.Error(), `step "exec"`) {
		t.Fatalf("want the failure naming the step, got %v", err)
	}
}

func TestExecutorBindingIsTheOneShotResolution(t *testing.T) {
	g, _ := executorEngine(t, "fake -p {system}", "ok")
	b, ok := g.ExecutorBinding()
	if !ok || b.Command != "fake -p {system}" {
		t.Errorf("ExecutorBinding = %+v, %v", b, ok)
	}
	g.oneShotAgents = nil
	if _, ok := g.ExecutorBinding(); ok {
		t.Errorf("no resolver wired must report no binding")
	}
}
