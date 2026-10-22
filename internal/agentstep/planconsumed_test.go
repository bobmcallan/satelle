package agentstep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_1b0233ba: when satelle dispatches a performer whose step consumes a
// document (apply_criteria.doc) and the payload carries it, the engine writes a
// plan-consumed row — attachment, content hash, attempt — BEFORE the performer
// runs, so the record never depends on the performer saying so.

const planBody = "# Plan\n\n1. do the thing\n"

// consumingWF allocates `plan` to a named performer whose step consumes the
// "plan" document, the way the coded step does.
var consumingWF = spineWF("", "", "",
	`plan|architect|architecture-alignment||||apply_criteria = { doc = "plan", heading = "Acceptance criteria" }`,
	"in_progress|executor",
	"done")

// producingWF is the same step without a consumed document, the way the
// readiness (planner) step is.
var producingWF = spineWF("", "", "",
	"plan|architect|architecture-alignment",
	"in_progress|executor",
	"done")

// snapRunner returns its queued outputs in order and records, at each Run, how
// many plan-consumed rows had been written and the payload it was handed.
type snapRunner struct {
	outs     []string
	rows     func() int
	rowsSeen []int
	payloads []map[string]any
}

func (r *snapRunner) Name() string    { return "snap" }
func (r *snapRunner) Command() string { return "snap" }
func (r *snapRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	i := len(r.rowsSeen)
	r.rowsSeen = append(r.rowsSeen, r.rows())
	var p map[string]any
	_ = json.Unmarshal([]byte(req.Payload), &p)
	r.payloads = append(r.payloads, p)
	if i >= len(r.outs) {
		i = len(r.outs) - 1
	}
	return []byte(r.outs[i]), nil
}

type consumedHarness struct {
	g      *Engine
	runner *snapRunner
	events []attemptEvent
}

func (h *consumedHarness) consumed() []attemptEvent {
	var out []attemptEvent
	for _, e := range h.events {
		if e.kind == verb.TelemetryPlanConsumed {
			out = append(out, e)
		}
	}
	return out
}

func newConsumedHarness(t *testing.T, wf, skill string, docs []DocState, outs ...string) *consumedHarness {
	t.Helper()
	h := &consumedHarness{}
	g, _ := newEngine(t, "", fakeDocs{workflow: wf, skillBody: skill, skillFound: true})
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == "architect" {
			return config.AgentBinding{Command: "snap", Tools: "read_file"}, true
		}
		return config.AgentBinding{}, false
	})
	h.runner = &snapRunner{outs: outs, rows: func() int { return len(h.consumed()) }}
	g.newRunner = func(string, string) (agentcli.Runner, error) { return h.runner, nil }
	g.SetTelemetry(func(_ context.Context, _, actor, kind string, data map[string]any) {
		h.events = append(h.events, attemptEvent{kind: kind, data: withActor(data, actor)})
	})
	g.SetDocsResolver(func(context.Context, string) []DocState { return docs })
	h.g = g
	return h
}

// withActor copies data with the writing actor, so a test can assert who the row names.
func withActor(data map[string]any, actor string) map[string]any {
	out := map[string]any{"_actor": actor}
	for k, v := range data {
		out[k] = v
	}
	return out
}

func dispatchPlan(t *testing.T, h *consumedHarness) {
	t.Helper()
	item := workitem.Item{ID: "sty_pc", Status: "backlog", AcceptanceCriteria: "1. first\n2. second"}
	if _, err := h.g.DispatchExecutor(context.Background(), item, "plan"); err != nil {
		t.Fatalf("DispatchExecutor: %v", err)
	}
}

func planAttached(t *testing.T, payload map[string]any) (value, present bool) {
	t.Helper()
	v, ok := payload["plan_attached"]
	if !ok {
		return false, false
	}
	b, isBool := v.(bool)
	if !isBool {
		t.Fatalf("plan_attached = %#v, want a bool", v)
	}
	return b, true
}

// AC1: one row, naming the attachment, its content hash and the attempt,
// written before the performer ran; the payload says the plan is attached.
func TestDispatchRecordsPlanConsumedBeforeThePerformerRuns(t *testing.T) {
	docs := []DocState{
		{Name: "step-summary-backlog-plan", Type: "step-summary", Body: "moved"},
		{Name: "plan", Type: "plan", Body: planBody},
	}
	h := newConsumedHarness(t, consumingWF, "rubric", docs, "done")
	dispatchPlan(t, h)

	rows := h.consumed()
	if len(rows) != 1 {
		t.Fatalf("plan-consumed rows = %d, want 1: %#v", len(rows), h.events)
	}
	sum := sha256.Sum256([]byte(planBody))
	want := map[string]any{
		"attachment":   "plan",
		"content_hash": "sha256:" + hex.EncodeToString(sum[:]),
		"attempt":      1,
		"step":         "plan",
		"source":       verb.TelemetrySourceDispatch,
		"_actor":       "architect",
	}
	for k, v := range want {
		if rows[0].data[k] != v {
			t.Errorf("row[%s] = %#v, want %#v", k, rows[0].data[k], v)
		}
	}
	if len(h.runner.rowsSeen) != 1 || h.runner.rowsSeen[0] != 1 {
		t.Fatalf("rows already written when the performer ran = %v, want [1]", h.runner.rowsSeen)
	}
	if got, present := planAttached(t, h.runner.payloads[0]); !present || !got {
		t.Errorf("plan_attached = (%t, present %t), want (true, true)", got, present)
	}
}

// AC1: each attempt of a bounded-attempt performer writes its own row, before
// that attempt runs.
func TestDispatchRecordsPlanConsumedPerAttempt(t *testing.T) {
	skill := strings.Replace(attemptedDispatchSkill, "attempt_max_total: 3", "attempt_max_total: 2", 1)
	docs := []DocState{{Name: "plan", Type: "plan", Body: planBody}}
	h := newConsumedHarness(t, consumingWF, skill, docs,
		validAttempt("invalid one"), validAttempt("## AC1\nok\n## AC2\nok"))
	h.g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		return name, typ, nil
	})
	dispatchPlan(t, h)

	rows := h.consumed()
	if len(rows) != 2 || rows[0].data["attempt"] != 1 || rows[1].data["attempt"] != 2 {
		t.Fatalf("rows = %#v, want attempts 1 and 2", rows)
	}
	if len(h.runner.rowsSeen) != 2 || h.runner.rowsSeen[0] != 1 || h.runner.rowsSeen[1] != 2 {
		t.Fatalf("rows written when each attempt ran = %v, want [1 2]", h.runner.rowsSeen)
	}
}

// AC2: the step consumes a document the payload does not carry — no row, and the
// payload says so.
func TestDispatchWithoutThePlanRecordsNothingAndSaysSo(t *testing.T) {
	docs := []DocState{{Name: "step-summary-backlog-plan", Type: "step-summary", Body: "moved"}}
	h := newConsumedHarness(t, consumingWF, "rubric", docs, "done")
	dispatchPlan(t, h)

	if rows := h.consumed(); len(rows) != 0 {
		t.Fatalf("plan-consumed rows = %#v, want none", rows)
	}
	if got, present := planAttached(t, h.runner.payloads[0]); !present || got {
		t.Errorf("plan_attached = (%t, present %t), want (false, true)", got, present)
	}
}

// The producing step declares no consumed document: a plan already attached to
// the story is not "consumed" by the step that writes it, and its payload says
// nothing about it.
func TestDispatchOfAStepConsumingNothingRecordsNothing(t *testing.T) {
	docs := []DocState{{Name: "plan", Type: "plan", Body: planBody}}
	h := newConsumedHarness(t, producingWF, "rubric", docs, "done")
	dispatchPlan(t, h)

	if rows := h.consumed(); len(rows) != 0 {
		t.Fatalf("plan-consumed rows = %#v, want none", rows)
	}
	if _, present := planAttached(t, h.runner.payloads[0]); present {
		t.Errorf("plan_attached is present on a step that consumes no document")
	}
}
