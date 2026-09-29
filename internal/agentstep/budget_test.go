package agentstep

// sty_a7914904: the engine hands a repo's turn budget to the harness, measures
// the run against the resolved budgets, and reports — it decides nothing. The
// numbers in these tests are fixture data.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestMeasureBudget(t *testing.T) {
	claudeCmd := config.AgentBinding{Command: "claude -p --max-turns {max_turns}"}
	claudeBare := config.AgentBinding{Command: "claude -p"}
	grokACP := config.AgentBinding{Interface: "acp", Command: "grok agent stdio"}
	usage := agentcli.UsageResult{Available: true, InputTokens: 900, Turns: 12, TurnsAvailable: true}

	t.Run("within budget", func(t *testing.T) {
		r := measureBudget(claudeCmd, config.Budget{Context: 1000, Turns: 12}, usage)
		if len(r.Overruns) != 0 || !r.TurnBudgetHandedToHarness || r.TurnBudgetNote != "" {
			t.Errorf("report = %+v, want no overrun and the harness handed the turn budget", r)
		}
		if r.Turns == nil || *r.Turns != 12 || r.ContextTokens == nil || *r.ContextTokens != 900 {
			t.Errorf("measurements = %v / %v", r.Turns, r.ContextTokens)
		}
	})
	t.Run("both exceeded", func(t *testing.T) {
		r := measureBudget(claudeCmd, config.Budget{Context: 500, Turns: 5}, usage)
		want := map[string][2]int{verb.BudgetKindTurns: {5, 12}, verb.BudgetKindContext: {500, 900}}
		if len(r.Overruns) != 2 {
			t.Fatalf("overruns = %+v", r.Overruns)
		}
		for _, o := range r.Overruns {
			if w := want[o.Kind]; o.Budget != w[0] || o.Measured != w[1] {
				t.Errorf("overrun %+v, want budget/measured %v", o, w)
			}
		}
	})
	t.Run("no budget still measures", func(t *testing.T) {
		r := measureBudget(claudeBare, config.Budget{}, usage)
		if r.Set() || len(r.Overruns) != 0 || r.Turns == nil || r.ContextTokens == nil {
			t.Errorf("report = %+v, want measurements only", r)
		}
	})
	t.Run("template without placeholder names the adapter", func(t *testing.T) {
		r := measureBudget(claudeBare, config.Budget{Turns: 5}, usage)
		if r.TurnBudgetHandedToHarness || !strings.Contains(r.TurnBudgetNote, "claude command") || !strings.Contains(r.TurnBudgetNote, "{max_turns}") {
			t.Errorf("note = %q handed=%v", r.TurnBudgetNote, r.TurnBudgetHandedToHarness)
		}
		if len(r.Overruns) != 1 {
			t.Errorf("the reported turns must still be checked when the harness was not told: %+v", r.Overruns)
		}
	})
	t.Run("grok acp limitation is adapter-named", func(t *testing.T) {
		r := measureBudget(grokACP, config.Budget{Turns: 5}, agentcli.UsageResult{Available: true, InputTokens: 1})
		if !strings.Contains(r.TurnBudgetNote, "grok agent stdio") {
			t.Errorf("note = %q, want the acp limitation", r.TurnBudgetNote)
		}
	})
	t.Run("unreported figures are unavailable, never zero", func(t *testing.T) {
		r := measureBudget(claudeCmd, config.Budget{Context: 1, Turns: 1}, agentcli.UsageResult{})
		if r.Turns != nil || r.ContextTokens != nil || len(r.Overruns) != 0 {
			t.Errorf("report = %+v, want nothing measured and no overrun", r)
		}
		if !strings.Contains(r.TurnsUnavailableReason, "claude command") || !strings.Contains(r.ContextUnavailableReason, "claude command") {
			t.Errorf("reasons must name the adapter: %q / %q", r.TurnsUnavailableReason, r.ContextUnavailableReason)
		}
	})
}

func budgetEnvelope(t *testing.T, turns int) string {
	t.Helper()
	env, err := json.Marshal(map[string]any{
		"result":    "done",
		"num_turns": turns,
		"usage":     map[string]any{"input_tokens": 100, "output_tokens": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(env)
}

// dispatchWithBudgets runs one named-agent dispatch under the given binding and
// [defaults], returning the runner (to read the Request it saw) and the result.
func dispatchWithBudgets(t *testing.T, binding config.AgentBinding, defaults config.Budget) (*fakeRunner, verb.DispatchResult) {
	t.Helper()
	wf := spineWF("", "", "",
		"in_progress|coder|planning-rubric",
		"done")
	const rubric = "---\nname: planning-rubric\ntype: skill\ndescription: test rubric\n---\nDo the work."
	g, _ := newEngine(t, "", fakeDocs{workflow: wf, skillBody: rubric, skillFound: true})
	r := &fakeRunner{out: budgetEnvelope(t, 12)}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return r, nil }
	binding.Tools = "read_file,grep,list_dir"
	if binding.Command == "" {
		binding.Command = "fake -p {system} --max-turns {max_turns}"
	}
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) { return binding, true })
	g.SetDefaultBudget(defaults)
	res, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_budget", Status: "backlog"}, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	return r, res
}

func TestDispatchExecutorPassesTurnBudgetAndReports(t *testing.T) {
	t.Run("binding budget reaches the runner and an overrun is reported", func(t *testing.T) {
		r, res := dispatchWithBudgets(t, config.AgentBinding{TurnBudget: 7, ContextBudget: 50}, config.Budget{Turns: 3})
		if r.got.MaxTurns != 7 {
			t.Errorf("Request.MaxTurns = %d, want the binding's 7 over [defaults] 3", r.got.MaxTurns)
		}
		if res.Budget == nil || len(res.Budget.Overruns) != 2 {
			t.Fatalf("budget report = %+v, want turns and context overruns", res.Budget)
		}
	})
	t.Run("defaults back an unset binding", func(t *testing.T) {
		r, res := dispatchWithBudgets(t, config.AgentBinding{}, config.Budget{Turns: 4})
		if r.got.MaxTurns != 4 || res.Budget == nil || res.Budget.TurnBudget != 4 {
			t.Errorf("MaxTurns = %d, report = %+v, want the [defaults] 4", r.got.MaxTurns, res.Budget)
		}
	})
	t.Run("no budget passes none and never overruns", func(t *testing.T) {
		r, res := dispatchWithBudgets(t, config.AgentBinding{}, config.Budget{})
		if r.got.MaxTurns != 0 {
			t.Errorf("Request.MaxTurns = %d, want 0: satelle ships no number", r.got.MaxTurns)
		}
		if res.Budget == nil || res.Budget.Set() || len(res.Budget.Overruns) != 0 || res.Budget.Turns == nil || *res.Budget.Turns != 12 {
			t.Errorf("budget report = %+v, want the measurement only", res.Budget)
		}
	})
}
