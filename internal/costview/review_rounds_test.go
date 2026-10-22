package costview

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// sty_46e950ab AC3/AC4: review rounds are a query over rows the binary already
// wrote — presentations per edge, and per seat attempt its outcome, turns and
// tool calls, with a reason for every figure that is unavailable.

func roundsEntry(kind string, payload map[string]any) ledger.Entry {
	b, _ := json.Marshal(payload)
	return ledger.Entry{ID: ledger.NewID(), Kind: kind, Payload: b}
}

func invocationEntry(attempt, skill, seat, outcome string, extra map[string]any) ledger.Entry {
	p := map[string]any{"from": "backlog", "to": "plan", "agent": "reviewer", "model": "opus", "skill": skill, "duration_ms": 1500, "usage_available": true}
	if attempt != "" {
		p["attempt"] = attempt
	}
	if seat != "" {
		p["seat"] = seat
	}
	if outcome != "" {
		p["outcome"] = outcome
	}
	for k, v := range extra {
		p[k] = v
	}
	return roundsEntry(ledger.KindAgentInvocation, p)
}

func verdictEntry(kind, attempt, skill string, seats []map[string]any) ledger.Entry {
	p := map[string]any{"from": "backlog", "to": "plan", "attempt": attempt, "skill": skill, "accept": kind == ledger.KindReviewAccept}
	if seats != nil {
		p["seats"] = seats
	}
	return roundsEntry(kind, p)
}

// seededRounds is backlog→plan presented three times: a panel whose claude seat
// rejects and whose grok seat is unavailable, a single seat cut off at the turn
// cap with no verdict, and a single-seat accept.
func seededRounds() []ledger.Entry {
	return []ledger.Entry{
		invocationEntry("att_1", "plan-review", "seat-claude", "", map[string]any{
			"turns": 4, "tool_calls_unavailable_reason": "claude command: the transport emits no tool events",
		}),
		invocationEntry("att_1", "plan-review", "seat-grok", "unavailable", map[string]any{
			"turns_unavailable_reason": "grok command: the run's output carried no turn count (num_turns)",
			"tool_calls":               0,
		}),
		verdictEntry(ledger.KindReviewReject, "att_1", "plan-review", []map[string]any{
			{"seat": "seat-claude", "accept": false},
			{"seat": "seat-grok", "accept": false, "unavailable": true},
		}),
		invocationEntry("att_2", "plan-review", "", "max-turns", map[string]any{
			"turns_unavailable_reason":      "grok command: the run's output carried no turn count (num_turns)",
			"tool_calls_unavailable_reason": "grok command: the transport emits no tool events",
		}),
		invocationEntry("att_3", "plan-review", "", "", map[string]any{"turns": 6, "tool_calls": 2}),
		verdictEntry(ledger.KindReviewAccept, "att_3", "plan-review", nil),
	}
}

func TestReviewRoundsReportsPresentationsAndSeatAttempts(t *testing.T) {
	rep := ReviewRounds("sty_a", "", seededRounds())
	if len(rep.Edges) != 1 {
		t.Fatalf("edges = %d, want backlog→plan only", len(rep.Edges))
	}
	e := rep.Edges[0]
	if e.From != "backlog" || e.To != "plan" || e.Presentations != 3 || e.Accepted != 1 || e.Rejected != 1 || e.NoVerdict != 1 {
		t.Fatalf("edge = %+v, want 3 presentations: 1 accepted, 1 rejected, 1 no verdict", e)
	}
	results := []string{e.Rounds[0].Result, e.Rounds[1].Result, e.Rounds[2].Result}
	if results[0] != ResultRejected || results[1] != ResultNoVerdict || results[2] != ResultAccepted {
		t.Errorf("results = %v", results)
	}

	panel := e.Rounds[0].Seats
	if len(panel) != 2 {
		t.Fatalf("panel seat attempts = %d", len(panel))
	}
	claude, grok := panel[0], panel[1]
	if claude.Seat != "seat-claude" || claude.Outcome != OutcomeReject || claude.Turns == nil || *claude.Turns != 4 || claude.DurationMs != 1500 {
		t.Errorf("claude seat = %+v, want a reject with 4 turns", claude)
	}
	if claude.ToolCalls != nil || claude.ToolCallsUnavailableReason == "" {
		t.Errorf("claude tool calls must be unavailable with its reason: %+v", claude)
	}
	if grok.Outcome != OutcomeUnavailable || grok.Turns != nil || grok.TurnsUnavailableReason == "" {
		t.Errorf("grok seat = %+v, want unavailable with no turn count and a reason", grok)
	}
	if grok.ToolCalls == nil || *grok.ToolCalls != 0 {
		t.Errorf("a measured zero must stay a zero, got %v", grok.ToolCalls)
	}

	capped := e.Rounds[1].Seats[0]
	if capped.Outcome != OutcomeMaxTurns || capped.Seat != "reviewer@opus" {
		t.Errorf("capped seat = %+v, want max-turns on the reviewer@opus seat", capped)
	}
	if got := e.Rounds[2].Seats[0]; got.Outcome != OutcomeAccept || got.Turns == nil || *got.Turns != 6 || got.ToolCalls == nil || *got.ToolCalls != 2 {
		t.Errorf("accepted seat = %+v", got)
	}
}

// A row written before turns and tool calls were recorded shows that, not a zero.
func TestReviewRoundsLegacyRowSaysItWasNotRecorded(t *testing.T) {
	// One gate run stamps its rows with one instant; that, and the skill, join a
	// row that predates attempt ids to its verdict.
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	legacy := invocationEntry("", "plan-review", "", "", nil)
	legacy.CreatedAt = at
	verdict := verdictEntry(ledger.KindReviewAccept, "att_1", "plan-review", nil)
	verdict.CreatedAt = at
	rep := ReviewRounds("sty_a", "", []ledger.Entry{legacy, verdict})
	seat := rep.Edges[0].Rounds[0].Seats[0]
	if seat.Turns != nil || seat.ToolCalls != nil || seat.TurnsUnavailableReason == "" || seat.ToolCallsUnavailableReason == "" {
		t.Errorf("a legacy row's figures are unavailable with a reason: %+v", seat)
	}
	if seat.Outcome != OutcomeAccept {
		t.Errorf("a legacy row still joins its verdict, outcome = %q", seat.Outcome)
	}
}

// A bundle's one invocation judged every skill; any reject rejects it.
func TestReviewRoundsBundleOutcomeIsRejectedWhenAnySkillRejects(t *testing.T) {
	rep := ReviewRounds("sty_a", "", []ledger.Entry{
		invocationEntry("att_1", "rev-a+rev-b", "", "", map[string]any{"bundle_skills": []string{"rev-a", "rev-b"}}),
		verdictEntry(ledger.KindReviewAccept, "att_1", "rev-a", nil),
		verdictEntry(ledger.KindReviewReject, "att_1", "rev-b", nil),
	})
	if got := rep.Edges[0].Rounds[0].Seats[0].Outcome; got != OutcomeReject {
		t.Errorf("bundle outcome = %q, want reject", got)
	}
}

func TestFamilyRoundsSumsAvailableFiguresAndCountsTheRest(t *testing.T) {
	a := ReviewRounds("sty_child_a", "", seededRounds())
	b := ReviewRounds("sty_child_b", "", []ledger.Entry{
		invocationEntry("att_1", "plan-review", "", "", map[string]any{"turns": 10, "tool_calls": 7}),
		verdictEntry(ledger.KindReviewAccept, "att_1", "plan-review", nil),
	})
	root := ReviewRounds("sty_root", "", nil)
	fam := FamilyRounds("sty_root", []ReviewRoundsReport{root, a, b})
	if len(fam.Total) != 1 {
		t.Fatalf("totals = %+v, want one edge", fam.Total)
	}
	got := fam.Total[0]
	want := EdgeTotal{
		From: "backlog", To: "plan",
		Presentations: 4, Accepted: 2, Rejected: 1, NoVerdict: 1,
		Attempts: SeatOutcomes{Accept: 2, Reject: 1, Unavailable: 1, MaxTurns: 1},
		// Turns 4 + 6 + 10 over available figures; the grok seat and the capped
		// seat gave none. Tool calls 0 + 2 + 7; the claude seat and the capped seat gave none.
		Turns: 20, UnavailableTurns: 2,
		ToolCalls: 9, UnavailableToolCalls: 2,
		Converged: 2, // both children's edges were accepted
	}
	if got != want {
		t.Errorf("family total = %+v\nwant           %+v", got, want)
	}
	if len(fam.Stories) != 3 || fam.Stories[1].StoryID != "sty_child_a" {
		t.Errorf("each story's own rounds are kept: %+v", fam.Stories)
	}
}
