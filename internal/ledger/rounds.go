package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"uuid"
)

// Presentation rounds and definition edits over a story's ledger (sty_5262592e).
//
// These are ENUMERATIONS of rows the binary already wrote — how many times an
// edge was presented and rejected, which definition edits postdate an accepted
// edge. Neither decides anything: the budget a count is compared against is a
// step's declared reject_budget, and whether an edit blocks an advance is a
// gate's configuration. Pure functions over entries, so they are testable
// without a store and callable from any layer that already holds the rows.

// NewAttemptID returns a fresh id for ONE presentation of an edge to its gate.
// Every verdict row a single gate run writes carries it, so a round is countable
// however many reviewers the gate fanned out to (parallel or serial).
func NewAttemptID() string { return fmt.Sprintf("att_%s", uuid.NewV4().String()[:8]) }

// NewBundleID returns a fresh id for ONE bundled reviewer session (sty_23e10d92):
// stamped on the session's single agent_invocation row and on every verdict row
// it produced, so the rows of one measured call can be found together.
func NewBundleID() string { return fmt.Sprintf("bun_%s", uuid.NewV4().String()[:8]) }

// edgeRow is the from/to/attempt/notes shape a review or transition row's
// payload carries. Extra keys are ignored.
type edgeRow struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Skill   string `json:"skill"`
	Notes   string `json:"notes"`
	Attempt string `json:"attempt"`
}

func decodeEdgeRow(e Entry) (edgeRow, bool) {
	var r edgeRow
	if len(e.Payload) == 0 || json.Unmarshal(e.Payload, &r) != nil {
		return edgeRow{}, false
	}
	return r, true
}

// RejectedRounds is the outcome of counting an edge's rejected presentations.
type RejectedRounds struct {
	// Rounds is the number of distinct presentations with at least one reject.
	Rounds int
	// LastSkill and LastNotes are the newest reject row's reviewer and objection.
	LastSkill string
	LastNotes string
}

// CountRejectedRounds counts the rejected PRESENTATIONS of the from→to edge in
// entries (oldest first). A presentation is one gate run, identified by the
// attempt id its rows carry; it is rejected when any of its rows is a reject, so
// a round where 3 of 4 reviewers reject counts once. A row written before attempt
// ids existed carries none and counts as a round of its own (conservative).
//
// The count restarts at every status_transition row of the story: a rejected
// presentation leaves the status unchanged, so consecutive rejected rounds have
// no transition between them, while a park, a resume or an accepted advance
// always writes one.
func CountRejectedRounds(entries []Entry, from, to string) RejectedRounds {
	var out RejectedRounds
	rejected := map[string]bool{}
	legacy := 0
	for _, e := range entries {
		switch e.Kind {
		case KindStatusTransition:
			out = RejectedRounds{}
			rejected = map[string]bool{}
			legacy = 0
		case KindReviewReject:
			r, ok := decodeEdgeRow(e)
			if !ok || r.From != from || r.To != to {
				continue
			}
			id := r.Attempt
			if id == "" {
				legacy++
				id = fmt.Sprintf("legacy-%d", legacy)
			}
			rejected[id] = true
			out.Rounds = len(rejected)
			out.LastSkill, out.LastNotes = r.Skill, r.Notes
		}
	}
	return out
}

// DefinitionEditedSince returns the definition_edited rows that postdate the
// latest status_transition from→to in entries (oldest first). With no such
// transition it returns nil: there is no accepted edge to have gone stale.
func DefinitionEditedSince(entries []Entry, from, to string) []Entry {
	at := -1
	for i, e := range entries {
		if e.Kind != KindStatusTransition {
			continue
		}
		if r, ok := decodeEdgeRow(e); ok && r.From == from && r.To == to {
			at = i
		}
	}
	if at < 0 {
		return nil
	}
	var out []Entry
	for _, e := range entries[at+1:] {
		if e.Kind == KindDefinitionEdited {
			out = append(out, e)
		}
	}
	return out
}

// ObjectionLine renders a reject as `skill: notes` for a blocked reason,
// collapsing whitespace so it stays one line.
func (r RejectedRounds) ObjectionLine() string {
	line := strings.Join(strings.Fields(r.LastNotes), " ")
	if r.LastSkill != "" {
		return r.LastSkill + ": " + line
	}
	return line
}
