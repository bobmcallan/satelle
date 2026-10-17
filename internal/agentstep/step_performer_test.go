package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_a32bd6a9: the document a readiness performer attaches is named by its
// skill's output_name, which StepPerformer reports; a skill with no output
// contract reports none, and a step nobody dispatches reports none.
func TestStepPerformerReportsTheSkillsOutputName(t *testing.T) {
	wf := spineWF("", "", "", "named|architect|architecture-alignment", "empty||", "done")
	bindings := map[string]config.AgentBinding{"architect": {Command: "fake -p {system}", Tools: "Read"}}
	const contractSkill = "---\nname: architecture-alignment\ntype: skill\ndescription: x\noutput_name: design\noutput_type: design-note\n---\nbody"
	cases := []struct {
		name, skill, to, want string
	}{
		{"declared", contractSkill, "named", "design"},
		{"no contract", "rubric", "named", ""},
		{"not dispatched", contractSkill, "empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newEngine(t, "ok", fakeDocs{workflow: wf, skillBody: tc.skill, skillFound: true})
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				b, ok := bindings[name]
				return b, ok
			})
			got, err := g.StepPerformer(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if got.OutputName != tc.want {
				t.Errorf("OutputName = %q, want %q", got.OutputName, tc.want)
			}
		})
	}
}

// sty_a32bd6a9: StepPerformer is the engine's one rule for who performs a step —
// the rule DispatchExecutor applies — exposed so `story ready` asks before it runs
// anything. Each way a step can have no dispatched performer, and the one way it
// can, is answered here, and DispatchExecutor must agree with every answer.
func TestStepPerformerIsTheRuleDispatchExecutorApplies(t *testing.T) {
	wf := spineWF("", "", "",
		"empty||",
		"exec|executor|",
		"judge|reviewer|",
		"named|architect|architecture-alignment",
		"asjudge|critic|architecture-alignment",
		"inloop|operator|architecture-alignment",
		"done")
	bindings := map[string]config.AgentBinding{
		"architect": {Command: "fake -p {system}", Tools: "Read,Bash(satelle:*)"},
		"critic":    {Role: config.RoleReviewer, Command: "fake -p {system}", Tools: "Read"},
		"operator":  {Command: "in-loop"},
	}
	cases := []struct {
		to         string
		dispatched bool
		reason     string // substring of the reason when nothing is dispatched
		errPart    string // substring of the error when the step is a broken definition
	}{
		{"empty", false, "agent is empty", ""},
		{"exec", false, "agent=executor", ""},
		{"judge", false, "agent=reviewer", ""},
		{"asjudge", false, "", "role=reviewer"},
		{"inloop", false, "in-loop", ""},
		{"named", true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.to, func(t *testing.T) {
			g, _ := newEngine(t, "ok", fakeDocs{workflow: wf, skillBody: "rubric", skillFound: true})
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				b, ok := bindings[name]
				return b, ok
			})
			g.newRunner = func(string, string) (agentcli.Runner, error) { return &fakeRunner{out: "ok"}, nil }
			item := workitem.Item{ID: "sty_1", Status: "backlog"}

			got, err := g.StepPerformer(context.Background(), item, tc.to)
			if tc.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("want an error naming %q, got %v", tc.errPart, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got.Dispatched != tc.dispatched {
				t.Fatalf("dispatched = %v (%s), want %v", got.Dispatched, got.Reason, tc.dispatched)
			}
			if !tc.dispatched && tc.reason != "" && !strings.Contains(got.Reason, tc.reason) {
				t.Errorf("reason %q should mention %q", got.Reason, tc.reason)
			}
			if tc.dispatched && got.Agent != "architect" {
				t.Errorf("agent = %q, want architect", got.Agent)
			}

			// DispatchExecutor agrees: it runs exactly when the rule dispatches, and
			// refuses exactly when the rule errors.
			res, derr := g.DispatchExecutor(context.Background(), item, tc.to)
			if (derr != nil) != (tc.errPart != "") {
				t.Fatalf("DispatchExecutor error = %v, rule error expected = %v", derr, tc.errPart != "")
			}
			if derr == nil && res.Dispatched != tc.dispatched {
				t.Errorf("DispatchExecutor dispatched = %v, StepPerformer said %v", res.Dispatched, tc.dispatched)
			}
		})
	}
}
