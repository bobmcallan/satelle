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
