package agentstep

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// findingNotesBudget bounds one finding's notes in the performer's payload. It is
// sized like a message body: a reviewer's full verdict reaches the performer
// whole, and only a longer one is cut, marked, and given a fetch command.
const findingNotesBudget = messageBodyBudget

// Finding is one gate seat's latest verdict on the edge a performer is
// re-presenting (sty_f948d345): what the performer is asked to answer. ID is
// <skill>[/<seat>]#<attempt> — stable, so the performer cites it in its answers.
// Blocking is a reject the performer is expected to adopt or rebut; an accept
// rides along so the performer sees the whole of the edge's latest round.
// Enumeration, not verdict: the binary lists the findings and records the
// answers, and how strictly a gate holds the performer to them is the gate's.
type Finding struct {
	ID             string `json:"id"`
	Skill          string `json:"skill"`
	Seat           string `json:"seat,omitempty"`
	Decision       string `json:"decision"`
	Blocking       bool   `json:"blocking"`
	Unavailable    bool   `json:"unavailable,omitempty"`
	Notes          string `json:"notes,omitempty"`
	NotesTruncated bool   `json:"notes_truncated,omitempty"`
	Attempt        int    `json:"attempt"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// FindingAnswers is the fact a gate is told about the performer's answers to the
// findings it was offered: which it answered and which it left unanswered. Go
// never rejects on it; the gate's rubric decides what a missing answer is worth.
type FindingAnswers struct {
	Answered []agentartifact.Answer `json:"answered,omitempty"`
	Missing  []string               `json:"missing"`
}

// numberAttempts assigns each verdict its 1-based attempt over the edge's FULL
// history. The one numbering rule, shared by the gate's prior verdicts and the
// performer's findings so an attempt means the same on both sides.
func numberAttempts(all []PriorVerdict) []PriorVerdict {
	for i := range all {
		all[i].Attempt = i + 1
	}
	return all
}

// fillFindings attaches, to the payload of a performer about to re-present the
// edge item.Status → toStatus, the latest verdict of every gate seat on that
// edge. Nothing is attached on a first presentation, or when the latest round of
// every gate accepted: there is nothing to answer. It runs AFTER fillPayloadDocs
// and never touches that counter.
func (g *Engine) fillFindings(ctx context.Context, item workitem.Item, toStatus string, tp *transitionPayload) {
	if g.priorVerdicts == nil || item.ID == "" {
		return
	}
	all := numberAttempts(g.priorVerdicts(ctx, item.ID, item.Status, toStatus, g.staleExemption(ctx, item, toStatus)))
	tp.Findings = latestFindings(item.ID, all)
}

// latestFindings lists the latest row of each gate skill on the edge, a panel's
// seats individually (seatPriorVerdicts), in the order the skills first appear.
// It is empty unless some skill's latest row rejected.
func latestFindings(itemID string, all []PriorVerdict) []Finding {
	latest := map[string]int{}
	var skills []string
	for i, v := range all {
		if _, seen := latest[v.Skill]; !seen {
			skills = append(skills, v.Skill)
		}
		latest[v.Skill] = i
	}
	var out []Finding
	rejected := false
	for _, skill := range skills {
		i := latest[skill]
		v := all[i]
		if v.Decision == "reject" {
			rejected = true
		}
		if len(v.Seats) == 0 {
			out = append(out, findingOf(itemID, v, ""))
			continue
		}
		for _, s := range v.Seats {
			if own := seatPriorVerdicts(all[i:i+1], skill, s.Seat); len(own) == 1 {
				out = append(out, findingOf(itemID, own[0], s.Seat))
			}
		}
	}
	if !rejected {
		return nil
	}
	return out
}

func findingOf(itemID string, v PriorVerdict, seat string) Finding {
	id := v.Skill
	if seat != "" {
		id += "/" + seat
	}
	f := Finding{
		ID: fmt.Sprintf("%s#%d", id, v.Attempt), Skill: v.Skill, Seat: seat, Decision: v.Decision,
		Blocking: v.Decision == "reject" && !v.Unavailable, Unavailable: v.Unavailable,
		Notes: v.Notes, Attempt: v.Attempt, CreatedAt: v.CreatedAt,
	}
	if len(f.Notes) > findingNotesBudget {
		f.Notes = excerpt(f.Notes, findingNotesBudget) + " — fetch the full verdict: satelle ledger list --story " + itemID
		f.NotesTruncated = true
	}
	return f
}

// blockingFindingIDs is the ids of the findings a performer is asked to answer.
func blockingFindingIDs(findings []Finding) []string {
	var ids []string
	for _, f := range findings {
		if f.Blocking {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

// RecordFindingAnswers carries the findings a performer was offered, and the
// answers it gave in stdout, onto its dispatch result, where dispatchPayload
// records them on the run's ledger row. A malformed answers block is an error
// like a malformed changes block. A performer with nothing to answer is a no-op.
func RecordFindingAnswers(res *verb.DispatchResult, offered []Finding, stdout []byte) error {
	ids := blockingFindingIDs(offered)
	if len(ids) == 0 {
		return nil
	}
	answers, err := agentartifact.DecodeAnswers(stdout)
	if err != nil {
		return err
	}
	res.FindingsOffered, res.Answers = ids, answers
	return nil
}

// FindingAnswersFrom reads, from a story's agent_invocation rows, the answers the
// latest dispatch of the edge from→to gave to the findings it was offered. The
// latest row by creation time wins; a row that offered no finding is not a
// dispatch of a re-presented edge and is skipped. Missing lists the offered ids
// with no answer. ok is false when no dispatch of the edge offered findings.
func FindingAnswersFrom(entries []ledger.Entry, from, to string) (fa FindingAnswers, ok bool) {
	var best *ledger.Entry
	var offered []string
	var answers []agentartifact.Answer
	for i := range entries {
		e := &entries[i]
		if e.Kind != ledger.KindAgentInvocation {
			continue
		}
		var row struct {
			From            string                 `json:"from"`
			To              string                 `json:"to"`
			FindingsOffered []string               `json:"findings_offered"`
			FindingAnswers  []agentartifact.Answer `json:"finding_answers"`
		}
		if json.Unmarshal(e.Payload, &row) != nil || row.From != from || row.To != to || len(row.FindingsOffered) == 0 {
			continue
		}
		if best == nil || !e.CreatedAt.Before(best.CreatedAt) {
			best, offered, answers = e, row.FindingsOffered, row.FindingAnswers
		}
	}
	if best == nil {
		return FindingAnswers{}, false
	}
	asked := map[string]bool{}
	for _, id := range offered {
		asked[id] = true
	}
	answered := map[string]bool{}
	fa.Missing = []string{}
	for _, a := range answers {
		if asked[a.Finding] && !answered[a.Finding] {
			answered[a.Finding] = true
			a.Evidence = excerpt(a.Evidence, findingNotesBudget)
			fa.Answered = append(fa.Answered, a)
		}
	}
	for _, id := range offered {
		if !answered[id] {
			fa.Missing = append(fa.Missing, id)
		}
	}
	return fa, true
}

// SetFindingAnswersResolver wires the resolver that reports the performer's
// answers to the findings of an edge (FindingAnswersFrom over the story's
// agent_invocation rows). Nil-safe: an unwired resolver injects nothing.
func (g *Engine) SetFindingAnswersResolver(fn func(ctx context.Context, itemID, from, to string) (FindingAnswers, bool)) {
	g.findingAnswers = fn
}

// fillFindingAnswers tells the gate judging item.Status → toStatus which of the
// findings its performer was offered went unanswered. A fact, never a verdict;
// absent when the edge's performer was offered nothing, so every other gate's
// payload is unchanged.
func (g *Engine) fillFindingAnswers(ctx context.Context, item workitem.Item, toStatus string, tp *transitionPayload) {
	if g.findingAnswers == nil || item.ID == "" {
		return
	}
	if fa, ok := g.findingAnswers(ctx, item.ID, item.Status, toStatus); ok {
		tp.FindingAnswers = &fa
	}
}
