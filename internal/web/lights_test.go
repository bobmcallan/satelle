package web

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// ev builds a ledger entry with a {from,to} payload.
func ev(kind, from, to string) ledger.Entry {
	p, _ := json.Marshal(lightPayload{From: from, To: to})
	return ledger.Entry{Kind: kind, Payload: p}
}

func states(ls []reviewLight) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.State
	}
	return out
}

// testStep is the step resolver for the simple test lifecycle
// open(0) → in_progress(1) → done(2).
func testStep(s string) int { return map[string]int{"in_progress": 1, "done": 2}[s] }

func TestBuildLights(t *testing.T) {
	chrono := []ledger.Entry{
		ev(ledger.KindReviewAccept, "open", "in_progress"),
		ev(ledger.KindStatusTransition, "open", "in_progress"),
		ev(ledger.KindReviewReject, "in_progress", "done"),
		ev(ledger.KindReviewAccept, "in_progress", "done"),
		ev(ledger.KindStatusTransition, "in_progress", "done"),
	}
	lights := buildLights(chrono, "done", testStep)
	// stage 1 passes; stage 2 fails then passes (shared index); no current (done).
	got := states(lights)
	want := []string{"pass", "fail", "pass"}
	if len(got) != len(want) {
		t.Fatalf("lights = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("light[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if lights[0].Index != 1 || lights[1].Index != 2 || lights[2].Index != 2 {
		t.Errorf("indices = %d,%d,%d, want 1,2,2", lights[0].Index, lights[1].Index, lights[2].Index)
	}
}

func TestBuildLightsNonTerminalTrailsCurrent(t *testing.T) {
	chrono := []ledger.Entry{
		ev(ledger.KindReviewAccept, "open", "in_progress"),
		ev(ledger.KindStatusTransition, "open", "in_progress"),
	}
	lights := buildLights(chrono, "in_progress", testStep)
	if len(lights) != 2 || lights[1].State != "current" || lights[1].Index != 2 {
		t.Fatalf("want [pass, current(2)], got %v", lights)
	}
}

func TestBuildLightsUngatedIsFired(t *testing.T) {
	// A status_transition with no matching review_accept is an ungated checkpoint.
	chrono := []ledger.Entry{ev(ledger.KindStatusTransition, "open", "in_progress")}
	lights := buildLights(chrono, "done", testStep)
	if len(lights) != 1 || lights[0].State != "fired" {
		t.Fatalf("want [fired], got %v", lights)
	}
}

func TestBuildLightsUnstartedHasNoCurrent(t *testing.T) {
	// A freshly-created item at its initial state (no transitions) shows NO lights
	// — the initial backlog/open state is not step 1, so no phantom current ①.
	if got := buildLights(nil, "open", testStep); len(got) != 0 {
		t.Fatalf("unstarted open item should have no lights, got %v", got)
	}
	if got := buildLights([]ledger.Entry{ev(ledger.KindStoryCreated, "", "")}, "open", testStep); len(got) != 0 {
		t.Fatalf("created-only item should have no lights, got %v", got)
	}
}

func TestBuildLightsNumbersByStepNotAppearance(t *testing.T) {
	// A ledger where a higher step is recorded before a lower one (e.g. a
	// corrected history). Numbers must follow the workflow STEP, not the order the
	// edge first appears — appearance order would give 1,2 here.
	chrono := []ledger.Entry{
		ev(ledger.KindStatusTransition, "in_progress", "done"), // step 2
		ev(ledger.KindStatusTransition, "open", "in_progress"), // step 1
	}
	lights := buildLights(chrono, "done", testStep)
	if len(lights) != 2 || lights[0].Index != 2 || lights[1].Index != 1 {
		t.Fatalf("want indices [2,1] by step, got %v", lights)
	}
}

func TestBuildLightsRetriedStepSharesNumber(t *testing.T) {
	// Step 1 (open→in_progress) rejected then accepted: both lights are step 1
	// (1 red then 1 green), with a current light at the next step.
	chrono := []ledger.Entry{
		ev(ledger.KindReviewReject, "open", "in_progress"),
		ev(ledger.KindReviewAccept, "open", "in_progress"),
		ev(ledger.KindStatusTransition, "open", "in_progress"),
	}
	lights := buildLights(chrono, "in_progress", testStep)
	if len(lights) != 3 {
		t.Fatalf("want 3 lights, got %v", lights)
	}
	if lights[0].Index != 1 || lights[0].State != "fail" {
		t.Errorf("light[0] = %v, want step 1 fail", lights[0])
	}
	if lights[1].Index != 1 || lights[1].State != "pass" {
		t.Errorf("light[1] = %v, want step 1 pass", lights[1])
	}
	if lights[2].State != "current" || lights[2].Index != 2 {
		t.Errorf("light[2] = %v, want current step 2", lights[2])
	}
}

func TestBuildLightsChronologicalAscending(t *testing.T) {
	// A forward run with a retry in the middle must render in ascending step
	// order, each light +0/+1 from the previous (the retried step repeats in
	// place): 1,2,3,3,4,5 — never reversed.
	step := func(s string) int { return map[string]int{"b": 1, "c": 2, "d": 3, "e": 4, "f": 5}[s] }
	chrono := []ledger.Entry{
		ev(ledger.KindStatusTransition, "a", "b"), // 1
		ev(ledger.KindStatusTransition, "b", "c"), // 2
		ev(ledger.KindReviewReject, "c", "d"),     // 3 (fail)
		ev(ledger.KindStatusTransition, "c", "d"), // 3 (retry)
		ev(ledger.KindStatusTransition, "d", "e"), // 4
		ev(ledger.KindStatusTransition, "e", "f"), // 5
	}
	lights := buildLights(chrono, "f", step) // non-terminal → trails a current at the next step
	var idx []int
	for _, l := range lights {
		idx = append(idx, l.Index)
	}
	// Completed steps 1,2,3,3,4,5 then the current (pulsing) light LAST at step 6.
	want := []int{1, 2, 3, 3, 4, 5, 6}
	if len(idx) != len(want) {
		t.Fatalf("indices = %v, want %v", idx, want)
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Errorf("light[%d] index = %d, want %d (order %v)", i, idx[i], want[i], idx)
		}
		if i > 0 && idx[i]-idx[i-1] > 1 {
			t.Errorf("non-sequential lights at %d: %d after %d (must be +0 or +1)", i, idx[i], idx[i-1])
		}
	}
	if last := lights[len(lights)-1]; last.State != "current" {
		t.Errorf("the last light must be the current stage, got %q", last.State)
	}
}

func TestGatedDepthsSpine(t *testing.T) {
	body := `
transitions:
  - {from: open, to: planned, reviewer_skill: "a"}
  - {from: planned, to: in_progress, reviewer_skill: "b"}
  - {from: in_progress, to: blocked}
  - {from: blocked, to: in_progress}
  - {from: in_progress, to: reviewed, reviewer_skill: "c"}
  - {from: reviewed, to: done, reviewer_skill: "d"}
`
	d := gatedDepths(parseWorkflow(body))
	for st, want := range map[string]int{"planned": 1, "in_progress": 2, "reviewed": 3, "done": 4} {
		if d[st] != want {
			t.Errorf("depth[%s] = %d, want %d", st, d[st], want)
		}
	}
	if _, ok := d["blocked"]; ok {
		t.Errorf("blocked must be off the gated spine, got depth %d", d["blocked"])
	}
}
