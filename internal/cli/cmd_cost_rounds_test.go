package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_46e950ab AC3/AC4: `story cost <id> --rounds` reports, per gated edge, the
// presentations and each seat attempt's turns, tool calls, outcome and duration —
// "—" with its reason where a figure is unavailable — and `--json` the same
// structure; an epic id adds each child and a family total per edge.

const (
	noTurnsReason     = "grok command: the run's output carried no turn count (num_turns)"
	noToolEventReason = "claude command: the transport emits no tool events"
)

// roundsRow is one ledger row to seed on backlog→plan.
type roundsRow struct {
	kind    string
	payload map[string]any
}

func inv(attempt, skill, seat, outcome string, extra map[string]any) roundsRow {
	p := map[string]any{"from": "backlog", "to": "plan", "agent": "reviewer", "model": "opus", "skill": skill, "attempt": attempt, "duration_ms": 2500, "usage_available": true}
	if seat != "" {
		p["seat"] = seat
	}
	if outcome != "" {
		p["outcome"] = outcome
	}
	for k, v := range extra {
		p[k] = v
	}
	return roundsRow{ledger.KindAgentInvocation, p}
}

func verdict(kind, attempt string, seats []map[string]any) roundsRow {
	p := map[string]any{"from": "backlog", "to": "plan", "attempt": attempt, "skill": "plan-review", "accept": kind == ledger.KindReviewAccept}
	if seats != nil {
		p["seats"] = seats
	}
	return roundsRow{kind, p}
}

// acceptRejectUnavailableMaxTurns holds all four seat outcomes over three
// presentations: a panel (claude rejects, grok unavailable), a single seat cut
// off at the turn cap with no verdict, and a single-seat accept.
func acceptRejectUnavailableMaxTurns() []roundsRow {
	return []roundsRow{
		inv("att_1", "plan-review", "seat-claude", "", map[string]any{"turns": 4, "tool_calls_unavailable_reason": noToolEventReason}),
		inv("att_1", "plan-review", "seat-grok", "unavailable", map[string]any{"turns_unavailable_reason": noTurnsReason, "tool_calls": 0}),
		verdict(ledger.KindReviewReject, "att_1", []map[string]any{
			{"seat": "seat-claude", "accept": false}, {"seat": "seat-grok", "accept": false, "unavailable": true},
		}),
		inv("att_2", "plan-review", "", "max-turns", map[string]any{"turns_unavailable_reason": noTurnsReason, "tool_calls_unavailable_reason": noToolEventReason}),
		inv("att_3", "plan-review", "", "", map[string]any{"turns": 6, "tool_calls": 2}),
		verdict(ledger.KindReviewAccept, "att_3", nil),
	}
}

// roundsRepo seeds a repo with an epic whose two children each carry rows, and
// returns the epic and child ids. The epic itself has none.
func roundsRepo(t *testing.T, children ...[]roundsRow) (epicID string, childIDs []string) {
	t.Helper()
	_ = tempRepo(t)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	at := time.Now().UTC()
	mk := func(title, parent string) string {
		it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: title, Status: "backlog", ParentID: parent}, at)
		if err != nil {
			t.Fatal(err)
		}
		return it.ID
	}
	epicID = mk("epic", "")
	for _, rows := range children {
		id := mk("child", epicID)
		childIDs = append(childIDs, id)
		for i, r := range rows {
			// Strictly increasing times: the ledger reads oldest first, and rows
			// sharing a timestamp have no defined order.
			b, _ := json.Marshal(r.payload)
			if _, err := a.Store.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: r.kind, Actor: "reviewer", Payload: b}, at.Add(time.Duration(i)*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	return epicID, childIDs
}

func TestStoryCostRoundsPrintsPresentationsAndSeatAttempts(t *testing.T) {
	_, kids := roundsRepo(t, acceptRejectUnavailableMaxTurns())
	out, err := runRoot(t, "story", "cost", kids[0], "--rounds")
	if err != nil {
		t.Fatalf("story cost --rounds: %v\n%s", err, out)
	}
	for _, want := range []string{
		kids[0] + " backlog→plan: 3 presentation(s) — 1 accepted, 1 rejected, 1 no verdict",
		"ATTEMPT", "TURNS", "TOOL CALLS", "OUTCOME", "DURATION",
		"seat-claude", "seat-grok", "reviewer@opus",
		"reject", "unavailable", "max-turns", "accept",
		"— (" + noTurnsReason + ")", "— (" + noToolEventReason + ")",
		"2.5s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--rounds output missing %q:\n%s", want, out)
		}
	}
	// The measured counts print as numbers; an unavailable one never as 0.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "seat-grok") && strings.Contains(line, "— ("+noTurnsReason+")") && !strings.Contains(line, "\t0") && !strings.Contains(line, "  0  ") {
			t.Errorf("the grok seat measured 0 tool calls and no turns: %q", line)
		}
	}
	if strings.Contains(out, "FAMILY TOTAL") {
		t.Errorf("a story with no children has no family section:\n%s", out)
	}
}

func TestStoryCostRoundsJSONHoldsTheSameStructure(t *testing.T) {
	_, kids := roundsRepo(t, acceptRejectUnavailableMaxTurns())
	out, err := runRoot(t, "story", "cost", kids[0], "--rounds", "--json")
	if err != nil {
		t.Fatalf("story cost --rounds --json: %v\n%s", err, out)
	}
	var rr verb.ReviewRounds
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if rr.StoryID != kids[0] || len(rr.Edges) != 1 {
		t.Fatalf("report = %+v", rr.ReviewRoundsReport)
	}
	e := rr.Edges[0]
	if e.Presentations != 3 || e.Accepted != 1 || e.Rejected != 1 || e.NoVerdict != 1 {
		t.Errorf("edge = %+v", e)
	}
	outcomes := map[string]int{}
	for _, r := range e.Rounds {
		for _, s := range r.Seats {
			outcomes[s.Outcome]++
		}
	}
	for _, want := range []string{costview.OutcomeAccept, costview.OutcomeReject, costview.OutcomeUnavailable, costview.OutcomeMaxTurns} {
		if outcomes[want] != 1 {
			t.Errorf("outcome %q seen %d times, want once: %v", want, outcomes[want], outcomes)
		}
	}
	grok := e.Rounds[0].Seats[1]
	if grok.Turns != nil || grok.TurnsUnavailableReason != noTurnsReason || grok.ToolCalls == nil || *grok.ToolCalls != 0 {
		t.Errorf("grok seat = %+v, want unavailable turns with the reason and a measured 0 tool calls", grok)
	}
	// An unavailable figure is JSON null beside its reason, never 0.
	if !strings.Contains(out, `"turns": null`) {
		t.Errorf("an unavailable turn count must serialise as null:\n%s", out)
	}
	if rr.Family != nil {
		t.Errorf("a story with no children carries no family: %+v", rr.Family)
	}
}

func TestStoryCostRoundsOnAnEpicAddsEachChildAndAFamilyTotal(t *testing.T) {
	childB := []roundsRow{
		inv("att_1", "plan-review", "", "", map[string]any{"turns": 10, "tool_calls": 7}),
		verdict(ledger.KindReviewAccept, "att_1", nil),
	}
	epic, kids := roundsRepo(t, acceptRejectUnavailableMaxTurns(), childB)

	out, err := runRoot(t, "story", "cost", epic, "--rounds")
	if err != nil {
		t.Fatalf("story cost <epic> --rounds: %v\n%s", err, out)
	}
	for _, want := range []string{
		kids[0] + " backlog→plan: 3 presentation(s)",
		kids[1] + " backlog→plan: 1 presentation(s)",
		"FAMILY TOTAL " + epic,
		"backlog→plan", "TURNS", "TOOL CALLS", "MAX-TURNS",
		"20 (2 unavailable)", "9 (2 unavailable)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("epic --rounds output missing %q:\n%s", want, out)
		}
	}

	jsonOut, err := runRoot(t, "story", "cost", epic, "--rounds", "--json")
	if err != nil {
		t.Fatalf("epic --rounds --json: %v\n%s", err, jsonOut)
	}
	var rr verb.ReviewRounds
	if err := json.Unmarshal([]byte(jsonOut), &rr); err != nil {
		t.Fatalf("parse json: %v\n%s", err, jsonOut)
	}
	if rr.Family == nil || len(rr.Family.Total) != 1 {
		t.Fatalf("family = %+v, want one edge total", rr.Family)
	}
	got := rr.Family.Total[0]
	want := costview.EdgeTotal{
		From: "backlog", To: "plan",
		Presentations: 4, Accepted: 2, Rejected: 1, NoVerdict: 1,
		Attempts: costview.SeatOutcomes{Accept: 2, Reject: 1, Unavailable: 1, MaxTurns: 1},
		Turns:    20, UnavailableTurns: 2, ToolCalls: 9, UnavailableToolCalls: 2,
		Converged: 2,
	}
	if got != want {
		t.Errorf("family total = %+v\nwant           %+v", got, want)
	}
	var ids []string
	for _, s := range rr.Family.Stories {
		ids = append(ids, s.StoryID)
	}
	if len(ids) != 3 || ids[0] != epic {
		t.Errorf("family stories = %v, want the epic then each child", ids)
	}
}

// sty_51d0f74a: a performer row on the edge, shaped like dispatchPayload's.
func perf(attempt string, budget map[string]any, extra map[string]any) roundsRow {
	p := map[string]any{"from": "backlog", "to": "plan", "agent": "planner", "skill": "plan", "attempt": attempt, "usage_available": true, "budget": budget}
	for k, v := range extra {
		p[k] = v
	}
	return roundsRow{ledger.KindAgentInvocation, p}
}

// performerRoundsRows is backlog→plan presented twice: round 1 rejected, round 2
// re-presented by a performer shown two findings that answered both (one adopted,
// one rebutted) and one it was never shown; the gate then accepts.
func performerRoundsRows() []roundsRow {
	return []roundsRow{
		perf("att_1", map[string]any{"turns": 7}, map[string]any{"tokens_in": 1000, "tokens_out": 200, "duration_ms": 3000}),
		inv("att_1", "plan-review", "", "", map[string]any{"turns": 4}),
		verdict(ledger.KindReviewReject, "att_1", nil),
		perf("att_2", map[string]any{"turns_unavailable_reason": noTurnsReason}, map[string]any{
			"tokens_in": 500, "tokens_out": 100, "duration_ms": 2000,
			"findings_offered": []string{"plan-review#1", "rev-b#1"},
			"finding_answers": []map[string]any{
				{"finding": "plan-review#1", "answer": "adopted"},
				{"finding": "rev-b#1", "answer": "rebutted", "evidence": "engine.go"},
				{"finding": "z#9", "answer": "adopted"},
			},
		}),
		inv("att_2", "plan-review", "", "", map[string]any{"turns": 3}),
		verdict(ledger.KindReviewAccept, "att_2", nil),
	}
}

func TestStoryCostRoundsPrintsThePerformerAndWhereTheEdgeConverged(t *testing.T) {
	_, kids := roundsRepo(t, performerRoundsRows())
	out, err := runRoot(t, "story", "cost", kids[0], "--rounds")
	if err != nil {
		t.Fatalf("story cost --rounds: %v\n%s", err, out)
	}
	for _, want := range []string{
		kids[0] + " backlog→plan: 2 presentation(s) — 1 accepted, 1 rejected, 0 no verdict — converged at round 2",
		"ROUND", "TOKENS IN/OUT",
		"performer planner",
		"1000/200", "3s", // round 1's tokens and duration
		"findings 0 answered 0 (adopted 0, rebutted 0)",
		"findings 2 answered 2 (adopted 1, rebutted 1)", // the stray z#9 answer is not counted
		"— (" + noTurnsReason + ")",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--rounds output missing %q:\n%s", want, out)
		}
	}
	var r1 string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "performer planner") && strings.Contains(line, "att_1") {
			r1 = line
		}
	}
	if !strings.Contains(r1, " 7 ") {
		t.Errorf("round 1's performer row must carry its 7 turns: %q", r1)
	}
}

func TestStoryCostRoundsJSONCarriesThePerformerOnTheRound(t *testing.T) {
	_, kids := roundsRepo(t, performerRoundsRows())
	out, err := runRoot(t, "story", "cost", kids[0], "--rounds", "--json")
	if err != nil {
		t.Fatalf("story cost --rounds --json: %v\n%s", err, out)
	}
	var rr verb.ReviewRounds
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	e := rr.Edges[0]
	if e.State != costview.EdgeConverged || e.ConvergedAt != 2 {
		t.Errorf("edge = %q converged at %d, want converged at 2", e.State, e.ConvergedAt)
	}
	p1, p2 := e.Rounds[0].Performer, e.Rounds[1].Performer
	if p1 == nil || p1.Turns == nil || *p1.Turns != 7 || p1.TokensIn != 1000 || p1.TokensOut != 200 || p1.DurationMs != 3000 {
		t.Errorf("round 1 performer = %+v", p1)
	}
	if p2 == nil || p2.Turns != nil || p2.TurnsUnavailableReason != noTurnsReason ||
		p2.FindingsPresented != 2 || p2.Answered != 2 || p2.Adopted != 1 || p2.Rebutted != 1 {
		t.Errorf("round 2 performer = %+v", p2)
	}
	for _, key := range []string{`"performer": {`, `"tokens_in": 1000`, `"turns": 7`, `"findings_presented": 2`, `"converged_at": 2`} {
		if !strings.Contains(out, key) {
			t.Errorf("--json missing %s:\n%s", key, out)
		}
	}
}

// An epic's family table totals the children's performers and edge states.
func TestStoryCostRoundsOnAnEpicTotalsThePerformers(t *testing.T) {
	stopped := performerRoundsRows()[:3] // one rejected round
	epic, _ := roundsRepo(t, performerRoundsRows(), stopped)
	out, err := runRoot(t, "story", "cost", epic, "--rounds")
	if err != nil {
		t.Fatalf("story cost <epic> --rounds: %v\n%s", err, out)
	}
	for _, want := range []string{"PERF TURNS", "PERF TOKENS IN/OUT", "PRESENTED", "ADOPTED", "REBUTTED", "CONVERGED", "STOPPED"} {
		if !strings.Contains(out, want) {
			t.Errorf("family table missing %q:\n%s", want, out)
		}
	}
	jsonOut, err := runRoot(t, "story", "cost", epic, "--rounds", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rr verb.ReviewRounds
	if err := json.Unmarshal([]byte(jsonOut), &rr); err != nil {
		t.Fatalf("parse json: %v\n%s", err, jsonOut)
	}
	got := rr.Family.Total[0]
	if got.PerformerTurns != 14 || got.UnavailablePerformerTurns != 1 || got.PerformerTokensIn != 2500 || got.PerformerTokensOut != 500 ||
		got.PerformerDurationMs != 8000 || got.FindingsPresented != 2 || got.Answered != 2 || got.Adopted != 1 || got.Rebutted != 1 {
		t.Errorf("family performer totals = %+v", got)
	}
	// Both children are still in backlog, the edge's from-state: one converged, one open.
	if got.Converged != 1 || got.Open != 1 || got.Stopped != 0 {
		t.Errorf("edge states = converged %d stopped %d open %d, want 1/0/1", got.Converged, got.Stopped, got.Open)
	}
}
