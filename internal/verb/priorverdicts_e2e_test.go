package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// captureRunner is an agent CLI that returns a scripted verdict per call and
// keeps every request, so a test can read exactly what rode the reviewer's stdin.
type captureRunner struct {
	outs []string
	reqs []agentcli.Request
}

func (c *captureRunner) Name() string    { return "capture" }
func (c *captureRunner) Command() string { return "capture -p --append-system-prompt {system}" }
func (c *captureRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	c.reqs = append(c.reqs, req)
	i := len(c.reqs) - 1
	if i >= len(c.outs) {
		i = len(c.outs) - 1
	}
	return []byte(c.outs[i]), nil
}

// pvReviewSkill is a minimal reviewer rubric that passes the structure and
// verdict-contract preflights the gate runs before it dispatches.
const pvReviewSkill = `---
name: pv-plan-review
type: skill
scope: project
tags: [type:skill, type:reviewer]
description: Fixture gate on plan → in_progress for the prior-verdict payload test.
---

# Fixture plan review

Judge the plan against the story.

Return JSON {"decision": "accept"|"reject", "notes": "…"}.
`

// pvRouteWorkflow gates plan → in_progress with the fixture reviewer.
var pvRouteWorkflow = routeHalves(
	`[feature]
obligations = ["raised", "planned", "coded", "closed"]
`,
	`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
requires = ["raised"]

[coded]
status = "in_progress"
reviewers = ["pv-plan-review"]
reviewer_agent = "reviewer"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

// TestSecondGateAttemptStdinCarriesPriorVerdict (sty_0f5e600c AC4): end to end,
// with the REAL engine as the transition gater and the REAL verb.PriorVerdicts as
// its resolver — a first attempt is judged with no memory, and the SECOND
// attempt's reviewer receives the first verdict's notes on stdin. The
// backlog→plan verdict seeded alongside must not appear (AC2).
func TestSecondGateAttemptStdinCarriesPriorVerdict(t *testing.T) {
	const (
		firstNotes = "PV-E2E-FIRST-VERDICT-MARKER: AC3 is unplanned"
		otherEdge  = "PV-E2E-OTHER-EDGE-MARKER"
	)
	db := wire(t)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))

	wfDir, skillDir := t.TempDir(), t.TempDir()
	writeRouteFiles(t, wfDir, pvRouteWorkflow)
	if err := os.WriteFile(filepath.Join(skillDir, "pv-plan-review.md"), []byte(pvReviewSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir, "skills": skillDir}})

	runner := &captureRunner{outs: []string{
		`{"decision":"reject","notes":"` + firstNotes + `"}`,
		`{"decision":"accept","notes":"the plan now covers AC3"}`,
	}}
	rev := agentstep.New(runner, db.DocIndex, t.TempDir(), "")
	// The same wiring internal/cli/app.go performs at both agentstep.New sites:
	// the engine's resolver IS verb.PriorVerdicts reading the ledger this
	// transition writes.
	rev.SetPriorVerdictsResolver(func(ctx context.Context, itemID, from, to string) []agentstep.PriorVerdict {
		verdicts, err := verb.PriorVerdicts(ctx, itemID, from, to)
		if err != nil {
			t.Errorf("PriorVerdicts: %v", err)
			return nil
		}
		out := make([]agentstep.PriorVerdict, 0, len(verdicts))
		for _, v := range verdicts {
			out = append(out, agentstep.PriorVerdict{Skill: v.Skill, Decision: v.Decision, Notes: v.Notes, CreatedAt: v.CreatedAt})
		}
		return out
	})
	verb.SetTransitionGater(rev)
	t.Cleanup(func() { verb.SetTransitionGater(nil); verb.SetStoryDir("") })

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "prior verdicts ride the payload", "category": "feature",
		"body": "b", "acceptance_criteria": "1. a",
	}), &story); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": story.ID, "status": "plan"})
	// A verdict on ANOTHER edge of the SAME story: it must never reach this edge's
	// reviewer.
	seedVerdict(t, story.ID, "review_reject", "backlog", "plan", "pv-intent-review", otherEdge)

	// Attempt 1 — the gate rejects, and verb records the verdict.
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
		t.Fatal("first attempt must be blocked by the reject")
	} else if !strings.Contains(err.Error(), firstNotes) {
		t.Fatalf("reject error should carry the verdict notes: %v", err)
	}

	// Attempt 2 — the live re-review.
	var after workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), &after); err != nil {
		t.Fatal(err)
	}
	if after.Status != "in_progress" {
		t.Fatalf("second attempt should enact: status = %q", after.Status)
	}
	if len(runner.reqs) != 2 {
		t.Fatalf("reviewer ran %d times, want 2", len(runner.reqs))
	}

	first, second := runner.reqs[0].Payload, runner.reqs[1].Payload
	if strings.Contains(first, "prior_verdicts") {
		t.Errorf("first attempt at the edge must carry no prior verdicts:\n%s", first)
	}
	if !strings.Contains(second, `"prior_verdicts"`) {
		t.Fatalf("second attempt's reviewer stdin missing prior_verdicts:\n%s", second)
	}
	for _, want := range []string{firstNotes, `"decision":"reject"`, `"attempt":1`, `"skill":"pv-plan-review"`} {
		if !strings.Contains(second, want) {
			t.Errorf("second attempt's stdin missing %q:\n%s", want, second)
		}
	}
	if strings.Contains(second, otherEdge) {
		t.Errorf("a verdict from another edge rode this edge's payload:\n%s", second)
	}
}
