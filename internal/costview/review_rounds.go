package costview

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// Review rounds (sty_46e950ab): how many times each gated edge was presented, and
// what each reviewer seat attempt in a presentation measured — its turns, tool
// calls, duration and outcome. Like GateValue this is a QUERY over rows the
// binary already wrote: it performs no I/O and decides nothing. A presentation is
// found by ledger.EdgePresentations; a seat attempt is a reviewer agent_invocation
// row decoded by DecodeRow, and its outcome is read from the record that owns it
// — the attempt's verdict rows, a panel's per-seat record, or the invocation
// row's own outcome for an attempt that produced no verdict.

// Seat attempt outcomes. A seat that judged is accept or reject; one that did not
// is unavailable (it failed or named no verdict) or max-turns (the harness ended
// it at its turn cap); no-verdict is a row no verdict row could be joined to.
const (
	OutcomeAccept      = "accept"
	OutcomeReject      = "reject"
	OutcomeUnavailable = "unavailable"
	OutcomeMaxTurns    = "max-turns"
	OutcomeNoVerdict   = "no-verdict"
)

// Presentation results, per ledger.Presentation.
const (
	ResultAccepted  = "accepted"
	ResultRejected  = "rejected"
	ResultNoVerdict = "no-verdict"
)

// legacyCountsReason is what a row written before turns and tool calls were
// recorded says of them: absent is unmeasured, never zero.
const legacyCountsReason = "not recorded: the row predates turn and tool-call counting"

// SeatAttempt is one reviewer seat's attempt within a presentation. Turns and
// ToolCalls are nil when unavailable, with the reason beside them.
type SeatAttempt struct {
	Skill                      string `json:"skill"`
	Seat                       string `json:"seat"`
	Outcome                    string `json:"outcome"`
	Turns                      *int   `json:"turns"`
	TurnsUnavailableReason     string `json:"turns_unavailable_reason,omitempty"`
	ToolCalls                  *int   `json:"tool_calls"`
	ToolCallsUnavailableReason string `json:"tool_calls_unavailable_reason,omitempty"`
	DurationMs                 int64  `json:"duration_ms"`
}

// PerformerAttempt is what the named performer that re-presented an edge cost in
// one round (sty_51d0f74a): its dispatch figures, summed when a retry ran, and
// how many of the blocking findings it was shown it answered. Turns is nil when
// any dispatch gave none, with that dispatch's reason beside it. An answer to an
// id that was not presented is not counted.
type PerformerAttempt struct {
	Agent                  string `json:"agent"`
	Skill                  string `json:"skill,omitempty"`
	Dispatches             int    `json:"dispatches"`
	Turns                  *int   `json:"turns"`
	TurnsUnavailableReason string `json:"turns_unavailable_reason,omitempty"`
	TokensIn               int    `json:"tokens_in"`
	TokensOut              int    `json:"tokens_out"`
	DurationMs             int64  `json:"duration_ms"`
	FindingsPresented      int    `json:"findings_presented"`
	Answered               int    `json:"answered"`
	Adopted                int    `json:"adopted"`
	Rebutted               int    `json:"rebutted"`
}

// RoundPresentation is one presentation of an edge: its 1-based round number, its
// attempt id, whether the verdicts accepted it, rejected it or never came, the
// performer that re-presented it (nil when no performer row is joined to it) and
// its seat attempts.
type RoundPresentation struct {
	Round     int               `json:"round"`
	Attempt   string            `json:"attempt"`
	Result    string            `json:"result"`
	Performer *PerformerAttempt `json:"performer,omitempty"`
	Seats     []SeatAttempt     `json:"seats"`
}

// Edge convergence states. An edge converged at its first accepted presentation;
// it stopped when it never was accepted and the story has left the edge's
// from-state; otherwise it is still open.
const (
	EdgeConverged = "converged"
	EdgeStopped   = "stopped"
	EdgeOpen      = "open"
)

// EdgeReviewRounds is one gated edge's presentations. Presentations is the
// count; Accepted, Rejected and NoVerdict split it. State is converged, stopped
// or open, and ConvergedAt / StoppedAt name the 1-based round it happened at
// (zero while the edge is not in that state).
type EdgeReviewRounds struct {
	From          string              `json:"from"`
	To            string              `json:"to"`
	Presentations int                 `json:"presentations"`
	Accepted      int                 `json:"accepted"`
	Rejected      int                 `json:"rejected"`
	NoVerdict     int                 `json:"no_verdict"`
	State         string              `json:"state"`
	ConvergedAt   int                 `json:"converged_at,omitempty"`
	StoppedAt     int                 `json:"stopped_at,omitempty"`
	Rounds        []RoundPresentation `json:"rounds"`
}

// ReviewRoundsReport is one story's review rounds, every gated edge in the order
// it first appears in the ledger.
type ReviewRoundsReport struct {
	StoryID string             `json:"story_id"`
	Edges   []EdgeReviewRounds `json:"edges"`
}

// SeatOutcomes counts seat attempts by outcome.
type SeatOutcomes struct {
	Accept      int `json:"accept"`
	Reject      int `json:"reject"`
	Unavailable int `json:"unavailable"`
	MaxTurns    int `json:"max_turns"`
	NoVerdict   int `json:"no_verdict"`
}

// EdgeTotal is one edge's figures summed across a family. Turns and ToolCalls
// sum the available figures only; UnavailableTurns and UnavailableToolCalls count
// the attempts that gave none, so a sum is never read as complete. The performer
// figures total the rounds' performers the same way, and Converged, Stopped and
// Open count the edges (one per story) in each state.
type EdgeTotal struct {
	From                 string       `json:"from"`
	To                   string       `json:"to"`
	Presentations        int          `json:"presentations"`
	Accepted             int          `json:"accepted"`
	Rejected             int          `json:"rejected"`
	NoVerdict            int          `json:"no_verdict"`
	Attempts             SeatOutcomes `json:"attempts"`
	Turns                int          `json:"turns"`
	UnavailableTurns     int          `json:"unavailable_turns"`
	ToolCalls            int          `json:"tool_calls"`
	UnavailableToolCalls int          `json:"unavailable_tool_calls"`

	PerformerTurns            int   `json:"performer_turns"`
	UnavailablePerformerTurns int   `json:"unavailable_performer_turns"`
	PerformerTokensIn         int   `json:"performer_tokens_in"`
	PerformerTokensOut        int   `json:"performer_tokens_out"`
	PerformerDurationMs       int64 `json:"performer_duration_ms"`
	FindingsPresented         int   `json:"findings_presented"`
	Answered                  int   `json:"answered"`
	Adopted                   int   `json:"adopted"`
	Rebutted                  int   `json:"rebutted"`
	Converged                 int   `json:"converged"`
	Stopped                   int   `json:"stopped"`
	Open                      int   `json:"open"`
}

// FamilyRoundsReport is an epic's review rounds: its own and each child's, then
// the per-edge totals across them.
type FamilyRoundsReport struct {
	RootID  string               `json:"root_id"`
	Stories []ReviewRoundsReport `json:"stories"`
	Total   []EdgeTotal          `json:"total"`
}

// verdictRow is the part of a review_accept/review_reject payload the outcome
// join reads: the skill, its verdict, and a panel's per-seat record.
type verdictRow struct {
	Skill  string `json:"skill"`
	Accept bool   `json:"accept"`
	Seats  []struct {
		Seat        string `json:"seat"`
		Accept      bool   `json:"accept"`
		Unavailable bool   `json:"unavailable"`
		MaxTurns    bool   `json:"max_turns"`
	} `json:"seats"`
}

// ReviewRounds builds a story's review rounds from its ledger entries (oldest
// first). status is the story's current status: an edge never accepted is stopped
// once the story has left its from-state, and open while it is still there or
// while the status is unknown (empty).
func ReviewRounds(storyID, status string, entries []ledger.Entry) ReviewRoundsReport {
	rep := ReviewRoundsReport{StoryID: storyID}
	for _, edge := range ledger.GatedEdges(entries) {
		er := EdgeReviewRounds{From: edge[0], To: edge[1]}
		presentations := ledger.EdgePresentations(entries, edge[0], edge[1])
		if len(presentations) == 0 {
			continue // only rows that evidence no presentation (a legacy summariser's)
		}
		for _, p := range presentations {
			rp := RoundPresentation{Round: len(er.Rounds) + 1, Attempt: p.Attempt, Performer: performerAttempt(entries, p), Seats: seatAttempts(entries, p)}
			switch {
			case p.NoVerdict:
				rp.Result = ResultNoVerdict
				er.NoVerdict++
			case p.Rejected:
				rp.Result = ResultRejected
				er.Rejected++
			default:
				rp.Result = ResultAccepted
				er.Accepted++
			}
			er.Presentations++
			er.Rounds = append(er.Rounds, rp)
			if rp.Result == ResultAccepted && er.ConvergedAt == 0 {
				er.ConvergedAt = rp.Round
			}
		}
		switch {
		case er.ConvergedAt > 0:
			er.State = EdgeConverged
		case status != "" && status != er.From:
			er.State, er.StoppedAt = EdgeStopped, len(er.Rounds)
		default:
			er.State = EdgeOpen
		}
		rep.Edges = append(rep.Edges, er)
	}
	return rep
}

// performerAttempt sums a presentation's performer dispatch rows into one
// PerformerAttempt, or nil when none is joined to it. The findings presented and
// answered are read from the last dispatch that was offered any, so a retry does
// not count them twice.
func performerAttempt(entries []ledger.Entry, p ledger.Presentation) *PerformerAttempt {
	var out *PerformerAttempt
	var offered []string
	var answers []agentartifact.Answer
	turns, measured := 0, true
	for _, i := range p.Performers {
		row, ok := DecodeRow(entries[i])
		if !ok {
			continue
		}
		if out == nil {
			out = &PerformerAttempt{Agent: row.Agent}
		}
		if out.Skill == "" {
			out.Skill = row.Skill
		}
		out.Dispatches++
		out.TokensIn += row.TokensIn
		out.TokensOut += row.TokensOut
		out.DurationMs += row.DurationMs
		switch {
		case row.Turns != nil:
			turns += *row.Turns
		case measured:
			measured = false
			out.TurnsUnavailableReason = row.TurnsUnavailableReason
		}
		var rec agentartifact.FindingAnswersRecord
		if json.Unmarshal(entries[i].Payload, &rec) == nil && len(rec.Offered) > 0 {
			offered, answers = rec.Offered, rec.Answers
		}
	}
	if out == nil {
		return nil
	}
	if measured {
		out.Turns = &turns
	} else if out.TurnsUnavailableReason == "" {
		out.TurnsUnavailableReason = legacyCountsReason
	}
	out.FindingsPresented = len(offered)
	answered, _ := agentartifact.MatchAnswers(offered, answers)
	for _, a := range answered {
		switch a.Answer {
		case agentartifact.AnswerAdopted:
			out.Adopted++
		case agentartifact.AnswerRebutted:
			out.Rebutted++
		}
	}
	out.Answered = out.Adopted + out.Rebutted
	return out
}

// seatAttempts decodes a presentation's reviewer invocation rows into seat
// attempts, oldest first.
func seatAttempts(entries []ledger.Entry, p ledger.Presentation) []SeatAttempt {
	verdicts := map[string][]verdictRow{} // skill -> the attempt's verdict rows
	for _, i := range p.Verdicts {
		var v verdictRow
		if json.Unmarshal(entries[i].Payload, &v) != nil {
			continue
		}
		if entries[i].Kind == ledger.KindReviewAccept || entries[i].Kind == ledger.KindReviewReject {
			verdicts[v.Skill] = append(verdicts[v.Skill], v)
		}
	}
	type stamped struct {
		at time.Time
		SeatAttempt
	}
	var found []stamped
	for _, i := range p.Invocations {
		row, ok := DecodeRow(entries[i])
		if !ok {
			continue
		}
		sa := SeatAttempt{
			Skill: row.Skill, Seat: row.Seat, Outcome: attemptOutcome(row, verdicts),
			Turns: row.Turns, TurnsUnavailableReason: row.TurnsUnavailableReason,
			ToolCalls: row.ToolCalls, ToolCallsUnavailableReason: row.ToolCallsUnavailableReason,
			DurationMs: row.DurationMs,
		}
		if sa.Seat == "" {
			sa.Seat = seatOf(row.Agent, row.Model)
		}
		if sa.Turns == nil && sa.TurnsUnavailableReason == "" {
			sa.TurnsUnavailableReason = legacyCountsReason
		}
		if sa.ToolCalls == nil && sa.ToolCallsUnavailableReason == "" {
			sa.ToolCallsUnavailableReason = legacyCountsReason
		}
		found = append(found, stamped{entries[i].CreatedAt, sa})
	}
	// One gate run stamps all its rows with one instant and the ledger breaks that
	// tie by id, which is arbitrary: order a run's seats by skill then seat so the
	// report reads the same every time.
	sort.SliceStable(found, func(a, b int) bool {
		x, y := found[a], found[b]
		switch {
		case !x.at.Equal(y.at):
			return x.at.Before(y.at)
		case x.Skill != y.Skill:
			return x.Skill < y.Skill
		}
		return x.Seat < y.Seat
	})
	out := make([]SeatAttempt, len(found))
	for i, f := range found {
		out[i] = f.SeatAttempt
	}
	return out
}

// attemptOutcome is one seat attempt's outcome, read from the record that owns
// it: the row's own outcome for an attempt that produced no verdict; a panel's
// per-seat record on its skill-level verdict; and for a single seat or a bundle
// the attempt's verdict rows for the skills it judged (any reject rejects).
func attemptOutcome(row Row, verdicts map[string][]verdictRow) string {
	if row.Outcome == OutcomeMaxTurns || row.Outcome == OutcomeUnavailable {
		return row.Outcome
	}
	if row.Seat != "" {
		for _, v := range verdicts[row.Skill] {
			for _, s := range v.Seats {
				if s.Seat != row.Seat {
					continue
				}
				switch {
				case s.MaxTurns:
					return OutcomeMaxTurns
				case s.Unavailable:
					return OutcomeUnavailable
				case s.Accept:
					return OutcomeAccept
				}
				return OutcomeReject
			}
		}
		return OutcomeNoVerdict
	}
	skills := row.BundleSkills
	if len(skills) == 0 {
		skills = []string{row.Skill}
	}
	outcome := OutcomeNoVerdict
	for _, s := range skills {
		for _, v := range verdicts[s] {
			if !v.Accept {
				return OutcomeReject
			}
			outcome = OutcomeAccept
		}
	}
	return outcome
}

// FamilyRounds folds a root's and its descendants' review rounds (one report per
// story, the root first) into per-edge totals. Turns and tool calls are summed
// over available figures only.
func FamilyRounds(rootID string, stories []ReviewRoundsReport) FamilyRoundsReport {
	out := FamilyRoundsReport{RootID: rootID, Stories: stories}
	index := map[[2]string]int{}
	for _, s := range stories {
		for _, e := range s.Edges {
			k := [2]string{e.From, e.To}
			n, ok := index[k]
			if !ok {
				n = len(out.Total)
				index[k] = n
				out.Total = append(out.Total, EdgeTotal{From: e.From, To: e.To})
			}
			t := &out.Total[n]
			t.Presentations += e.Presentations
			t.Accepted += e.Accepted
			t.Rejected += e.Rejected
			t.NoVerdict += e.NoVerdict
			switch e.State {
			case EdgeConverged:
				t.Converged++
			case EdgeStopped:
				t.Stopped++
			default:
				t.Open++
			}
			for _, r := range e.Rounds {
				if pa := r.Performer; pa != nil {
					if pa.Turns != nil {
						t.PerformerTurns += *pa.Turns
					} else {
						t.UnavailablePerformerTurns++
					}
					t.PerformerTokensIn += pa.TokensIn
					t.PerformerTokensOut += pa.TokensOut
					t.PerformerDurationMs += pa.DurationMs
					t.FindingsPresented += pa.FindingsPresented
					t.Answered += pa.Answered
					t.Adopted += pa.Adopted
					t.Rebutted += pa.Rebutted
				}
				for _, a := range r.Seats {
					switch a.Outcome {
					case OutcomeAccept:
						t.Attempts.Accept++
					case OutcomeReject:
						t.Attempts.Reject++
					case OutcomeUnavailable:
						t.Attempts.Unavailable++
					case OutcomeMaxTurns:
						t.Attempts.MaxTurns++
					default:
						t.Attempts.NoVerdict++
					}
					if a.Turns != nil {
						t.Turns += *a.Turns
					} else {
						t.UnavailableTurns++
					}
					if a.ToolCalls != nil {
						t.ToolCalls += *a.ToolCalls
					} else {
						t.UnavailableToolCalls++
					}
				}
			}
		}
	}
	return out
}
