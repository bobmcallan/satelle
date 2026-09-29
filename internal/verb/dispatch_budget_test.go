package verb

// sty_a7914904 AC4/AC5: a dispatched performer's measurement against the
// repo's budgets. With none configured satelle only warns and records and never
// parks; with one configured, an overrun is recorded and routes to rework only
// when the step declares a rework loop, otherwise the story is blocked and the
// reason is kept on the ledger. The numbers here are fixture data.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// budgetDispatcher is a scripted ExecutorDispatcher returning a fixed report.
type budgetDispatcher struct{ report *BudgetReport }

func (d budgetDispatcher) DispatchExecutor(_ context.Context, _ workitem.Item, to string) (DispatchResult, error) {
	if to != "in_progress" {
		return DispatchResult{}, nil // a park state performs nothing, as in the engine
	}
	return DispatchResult{Dispatched: true, Agent: "coder", Command: "fake", Skill: "coder", Budget: d.report}, nil
}

// authoredBudgetRoute indexes a route whose coded step is allocated to "coder"
// and whose done.toml offers a blocked park state unless noPark is set.
func authoredBudgetRoute(t *testing.T, codedExtra string, noPark bool) *engageHarness {
	t.Helper()
	h := newEngageHarness(t, "")
	dir := t.TempDir()
	park := "park = { state = \"blocked\", gate = \"park-gate\" }\n"
	if noPark {
		park = ""
	}
	done := "[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n" + park
	step := "[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n" +
		"[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nrequires = [\"raised\"]\n" + codedExtra + "\n\n" +
		"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n"
	for name, body := range map[string]string{"done.toml": done, "step.toml": step} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.db.DocIndex.Sync(context.Background(), map[string]string{"workflows": dir}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *engageHarness) dispatchWith(report *BudgetReport) {
	SetExecutorDispatcher(budgetDispatcher{report: report})
	h.t.Cleanup(func() { SetExecutorDispatcher(nil) })
}

func (h *engageHarness) status(id string) string {
	h.t.Helper()
	it, err := h.db.Stories.Get(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return it.Status
}

func (h *engageHarness) overruns(id string) []BudgetOverrunPayload {
	h.t.Helper()
	entries, err := h.db.Ledger.ListByStory(context.Background(), id, ledger.KindBudgetOverrun)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []BudgetOverrunPayload
	for _, e := range entries {
		var p BudgetOverrunPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// AC4: no budget → warn and record; the story is never parked for spend.
func TestDispatchNoBudgetOnlyWarnsAndRecords(t *testing.T) {
	h := newEngageHarness(t, "")
	h.dispatchWith(&BudgetReport{Turns: intp(500), ContextTokens: intp(9_000_000)})
	it := h.create("unbudgeted")
	h.lines = nil

	h.set(it.ID, "in_progress") // asserts the transition enacted
	if got := h.status(it.ID); got != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got)
	}
	if o := h.overruns(it.ID); len(o) != 0 {
		t.Errorf("no budget configured, yet overrun rows: %+v", o)
	}
	if !h.warned("no context_budget or turn_budget is configured") || !h.warned("500 turns") {
		t.Errorf("must warn with the measurement, got %q", h.lines)
	}
	entries, _ := h.db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindAgentInvocation)
	var recorded bool
	for _, e := range entries {
		if strings.Contains(string(e.Payload), `"budget"`) && strings.Contains(string(e.Payload), `"turns":500`) {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("the measurement must be recorded on the agent_invocation row: %v", entries)
	}
}

// AC5: a configured bound, exceeded, on a step with NO rework key → blocked,
// the reason kept on the ledger.
func TestDispatchOverrunBlocksWithoutRework(t *testing.T) {
	h := authoredBudgetRoute(t, "", false)
	h.dispatchWith(&BudgetReport{TurnBudget: 10, Turns: intp(31),
		Overruns: []BudgetOverrun{{Kind: BudgetKindTurns, Budget: 10, Measured: 31}}})
	it := h.create("overrun without rework")
	h.lines = nil

	h.dispatch("story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if got := h.status(it.ID); got != "blocked" {
		t.Fatalf("status = %q, want blocked", got)
	}
	o := h.overruns(it.ID)
	if len(o) != 1 || o[0].Consequence != OverrunBlocked || o[0].Kind != BudgetKindTurns || o[0].Measured != 31 || o[0].Budget != 10 {
		t.Fatalf("overrun rows = %+v, want one blocked turns 31 > 10", o)
	}
	if !strings.Contains(o[0].Reason, "configured by this repo") {
		t.Errorf("reason must say the repo configured the bound: %q", o[0].Reason)
	}
	comments, _ := h.db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindComment)
	var kept bool
	for _, c := range comments {
		kept = kept || strings.Contains(c.Body, o[0].Reason)
	}
	if !kept {
		t.Errorf("the reason must be kept on the ledger as the parked story's comment: %v", comments)
	}
}

// AC5: the same overrun on a step that DECLARES rework routes to rework — the
// story is not parked and the line names the relay.
func TestDispatchOverrunRoutesToReworkWhenDeclared(t *testing.T) {
	h := authoredBudgetRoute(t, `rework = { consult = "reviewer", rounds = 2 }`, false)
	h.dispatchWith(&BudgetReport{ContextBudget: 100, ContextTokens: intp(250),
		Overruns: []BudgetOverrun{{Kind: BudgetKindContext, Budget: 100, Measured: 250}}})
	it := h.create("overrun with rework")
	h.lines = nil

	h.dispatch("story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if got := h.status(it.ID); got != "in_progress" {
		t.Fatalf("status = %q, want in_progress (rework, not blocked)", got)
	}
	o := h.overruns(it.ID)
	if len(o) != 1 || o[0].Consequence != OverrunRework || o[0].Kind != BudgetKindContext {
		t.Fatalf("overrun rows = %+v, want one rework context row", o)
	}
	if !h.warned("satelle story rework " + it.ID) {
		t.Errorf("output must name the rework relay: %q", h.lines)
	}
}

// AC5: nothing to park in → the transition is refused, no status invented.
func TestDispatchOverrunWithNoBlockedStateRefuses(t *testing.T) {
	h := authoredBudgetRoute(t, "", true)
	h.dispatchWith(&BudgetReport{TurnBudget: 3, Turns: intp(9),
		Overruns: []BudgetOverrun{{Kind: BudgetKindTurns, Budget: 3, Measured: 9}}})
	it := h.create("overrun nowhere to park")

	b, _ := json.Marshal(map[string]any{"id": it.ID, "status": "in_progress"})
	if _, err := Dispatch(context.Background(), "story-set", b); err == nil || !strings.Contains(err.Error(), "no blocked state") {
		t.Fatalf("err = %v, want a refusal naming the missing blocked state", err)
	}
	if got := h.status(it.ID); got != "backlog" {
		t.Errorf("status = %q, want backlog (nothing invented)", got)
	}
	if o := h.overruns(it.ID); len(o) != 1 || o[0].Consequence != OverrunRefused {
		t.Errorf("overrun rows = %+v, want one refused", o)
	}
}

// A bound set but a figure the run did not report is never an overrun: the
// report carries no Overruns and nothing parks.
func TestDispatchBudgetSetButUnmeasuredDoesNothing(t *testing.T) {
	h := authoredBudgetRoute(t, "", false)
	h.dispatchWith(&BudgetReport{TurnBudget: 3, TurnsUnavailableReason: "claude command: no turn count",
		TurnBudgetNote: "claude command: the command template carries no {max_turns} placeholder"})
	it := h.create("unmeasured")
	h.lines = nil
	h.set(it.ID, "in_progress")
	if o := h.overruns(it.ID); len(o) != 0 {
		t.Errorf("an unreported figure must not read as an overrun: %+v", o)
	}
	if !h.warned("carries no {max_turns} placeholder") {
		t.Errorf("the harness limitation must be surfaced, got %q", h.lines)
	}
}

// The step's own declaration wins over the binding, which wins over [defaults].
func TestResolveStepBudgetLadder(t *testing.T) {
	h := authoredBudgetRoute(t, "context_budget = 7", false)
	SetAgentBudgets(func(agent string) config.Budget {
		if agent != "coder" {
			t.Errorf("resolver asked about %q, want the step's allocated agent coder", agent)
		}
		return config.Budget{Context: 500, Turns: 20}
	})
	it := h.create("ladder")
	got := resolveStepBudget(context.Background(), it, "in_progress")
	if got != (config.Budget{Context: 7, Turns: 20}) {
		t.Errorf("resolveStepBudget = %+v, want step context 7 over binding, binding turns 20", got)
	}
	if got := resolveStepBudget(context.Background(), it, "no_such_step"); got.Set() {
		t.Errorf("an unknown step resolves no budget, got %+v", got)
	}
}
