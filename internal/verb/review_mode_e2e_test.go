package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_93359e0f end to end, with the real engine as the gater and the real
// verb.PriorVerdicts as its resolver: a fail-fast gate's reject is stored with
// its mode and coverage, and the next presentation is told the prior was partial.

// failFastRoute is the prior-verdict fixture route with its gate declared
// fail-fast on the step.
func failFastRoute() map[string]string {
	route := map[string]string{}
	for k, v := range pvRouteWorkflow {
		route[k] = v
	}
	route["step"] = strings.Replace(route["step"], `reviewer_agent = "reviewer"`, `reviewer_agent = "reviewer"`+"\n"+`fail_fast = ["pv-plan-review"]`, 1)
	return route
}

// claudeResult wraps a verdict in the envelope `claude -p --output-format json`
// prints, so the reviewer path reports num_turns the way it does in production.
func claudeResult(t *testing.T, verdict string, turns int) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"result": verdict, "num_turns": turns,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const stoppedAt = "AC2 has no plan entry"

func failFastReject(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"decision": "reject", "notes": stoppedAt, "reviewed": "AC2",
		"coverage": map[string]any{"complete": false, "stopped_at": stoppedAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFailFastRejectIsStoredPartialAndReachesTheNextPresentation(t *testing.T) {
	runner, story := newPVHarnessOn(t, failFastRoute(), []string{
		claudeResult(t, failFastReject(t), 3),
		verdictJSON("accept", "the plan now covers AC2 and the rest", "whole plan"),
	}, nil)

	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
		t.Fatal("the fail-fast reject must block the edge")
	}

	// AC2: the seat was told the mode.
	var item map[string]any
	if err := json.Unmarshal([]byte(runner.reqs[0].Payload), &item); err != nil {
		t.Fatal(err)
	}
	if item["review_mode"] != "fail-fast" {
		t.Errorf("work item review_mode = %v, want fail-fast", item["review_mode"])
	}

	// AC3: the verdict row stores the mode and the coverage.
	rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
	if len(rows) != 1 {
		t.Fatalf("want one reject row, got %d", len(rows))
	}
	cov, _ := rows[0]["coverage"].(map[string]any)
	if rows[0]["review_mode"] != "fail-fast" || cov["complete"] != false || cov["stopped_at"] != stoppedAt {
		t.Errorf("reject row = %v, want review_mode fail-fast and coverage stopped at %q", rows[0], stoppedAt)
	}

	// AC6: the invocation row carries the seat's reported turns.
	inv := reviewPayloads(t, story.ID, ledger.KindAgentInvocation)
	if len(inv) != 1 || inv[0]["turns"] != float64(3) {
		t.Errorf("agent_invocation rows = %v, want one with turns 3", inv)
	}

	// AC4: the read the next presentation gets is partial, and says where.
	prior, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress", verb.StaleExemption{})
	if err != nil || len(prior) != 1 {
		t.Fatalf("PriorVerdicts = %v, %v", prior, err)
	}
	if !prior[0].Partial || prior[0].Coverage == nil || prior[0].Coverage.StoppedAt != stoppedAt || prior[0].ReviewMode != "fail-fast" {
		t.Errorf("prior verdict = %+v, want partial, fail-fast, stopped at %q", prior[0], stoppedAt)
	}

	// The re-presented seat reads it in its prior_verdicts file.
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err != nil {
		t.Fatalf("second presentation: %v", err)
	}
	var priors []agentstep.PriorVerdict
	if err := json.Unmarshal([]byte(runner.priorFiles[1]), &priors); err != nil || len(priors) != 1 {
		t.Fatalf("prior_verdicts.json: %v\n%s", err, runner.priorFiles[1])
	}
	if !priors[0].Partial || priors[0].Coverage == nil || priors[0].Coverage.StoppedAt != stoppedAt {
		t.Errorf("the seat's prior = %+v, want partial with the stopped-at finding", priors[0])
	}
}

// A full-mode reject, and a fail-fast accept, are not partial: only an early
// stop leaves a remainder unreviewed.
func TestFullRejectAndFailFastAcceptAreNotPartial(t *testing.T) {
	t.Run("full reject", func(t *testing.T) {
		runner, story := newPVHarness(t, []string{
			verdictJSON("reject", "AC2 and AC3 are unplanned", "the plan"),
			verdictJSON("accept", "now covered", "the plan v2"),
		}, nil)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want the reject")
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
		if _, ok := rows[0]["review_mode"]; ok {
			t.Errorf("a gate with no declared mode records none: %v", rows[0])
		}
		if _, ok := rows[0]["coverage"]; ok {
			t.Errorf("a verdict that states no coverage stores none: %v", rows[0])
		}
		prior, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress", verb.StaleExemption{})
		if err != nil || len(prior) != 1 || prior[0].Partial {
			t.Errorf("prior = %+v, %v: a full reject is not partial", prior, err)
		}
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(runner.priorFiles[1], `"partial"`) {
			t.Errorf("a full-mode prior must not be marked partial:\n%s", runner.priorFiles[1])
		}
	})
	t.Run("fail-fast accept", func(t *testing.T) {
		_, story := newPVHarnessOn(t, failFastRoute(), []string{verdictJSON("accept", "nothing blocking", "the plan")}, nil)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err != nil {
			t.Fatal(err)
		}
		prior, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress", verb.StaleExemption{})
		if err != nil || len(prior) != 1 || prior[0].Partial {
			t.Errorf("prior = %+v, %v: an accept in fail-fast mode reviewed everything", prior, err)
		}
	})
}
