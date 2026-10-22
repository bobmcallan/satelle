package agentstep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	// quotes are the complete quotations the verdict recorded: the seat's
	// reviewed and each rejected criterion entry's. They are what the finding
	// names (sty_73224b86); never serialised, so the performer's payload is
	// unchanged. Empty when none is complete (an unavailable, stale or truncated
	// verdict), which the progress rule reads as "names nothing it can check".
	quotes []string
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
	tp.Findings = FindingsFrom(item.ID, g.priorVerdicts(ctx, item.ID, item.Status, toStatus, g.staleExemption(ctx, item, toStatus)))
}

// FindingsFrom is the findings a performer re-presenting the edge is offered,
// given the edge's prior verdicts oldest first (what the engine's resolver
// returns). Exported so a test can build them from a ledger prior and drive them
// through RecordFindingProgress, as the engine does.
func FindingsFrom(itemID string, priors []PriorVerdict) []Finding {
	return latestFindings(itemID, numberAttempts(priors))
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
		Notes: v.Notes, Attempt: v.Attempt, CreatedAt: v.CreatedAt, quotes: findingQuotes(v),
	}
	if len(f.Notes) > findingNotesBudget {
		f.Notes = excerpt(f.Notes, findingNotesBudget) + " — fetch the full verdict: satelle ledger list --story " + itemID
		f.NotesTruncated = true
	}
	return f
}

// findingQuotes lists the complete quotations a verdict rests on: its own
// reviewed text and that of each rejected criterion entry. A stale, unavailable
// or truncated quotation is not complete and is left out.
func findingQuotes(v PriorVerdict) []string {
	if v.Unavailable || v.ReviewedStale {
		return nil
	}
	var out []string
	if !v.ReviewedTruncated && strings.TrimSpace(v.Reviewed) != "" {
		out = append(out, v.Reviewed)
	}
	for _, c := range v.Criteria {
		if c.Status == verb.CriterionRejected && !c.ReviewedTruncated && strings.TrimSpace(c.Reviewed) != "" {
			out = append(out, c.Reviewed)
		}
	}
	return out
}

// ProgressMaterial is what a re-presented output is judged against: Judged is
// the material the gates will read with the new output in place (the story card
// and every document, the step's output replaced by the new one), Output the new
// output document and Prior the one it replaces. HasOutput is false when the
// step returned no output document, which never reads as a change.
type ProgressMaterial struct {
	Judged    string
	Output    string
	Prior     string
	HasOutput bool
}

// computeProgress decides, per blocking finding offered, whether the output
// answers it or changes what it names. Answered: the performer replied to it.
// Changed: a complete quotation of it is no longer present in the judged
// material (quotationPresent, the carry rule's word match); a finding with no
// complete quotation is changed only when the output differs byte for byte from
// the one it replaces. Enumeration only — the step's policy is the verb's.
func computeProgress(offered []Finding, answers []agentartifact.Answer, m ProgressMaterial) *verb.FindingProgress {
	answered := map[string]bool{}
	for _, a := range answers {
		answered[a.Finding] = true
	}
	var p verb.FindingProgress
	for _, f := range offered {
		if !f.Blocking {
			continue
		}
		p.Offered = append(p.Offered, f.ID)
		if findingProgressed(f, answered[f.ID], m) {
			p.Progressed = append(p.Progressed, f.ID)
		} else {
			p.Unprogressed = append(p.Unprogressed, f.ID)
		}
	}
	if len(p.Offered) == 0 {
		return nil
	}
	return &p
}

func findingProgressed(f Finding, answered bool, m ProgressMaterial) bool {
	if answered {
		return true
	}
	if len(f.quotes) == 0 {
		return m.HasOutput && m.Output != m.Prior
	}
	for _, q := range f.quotes {
		if !quotationPresent(q, m.Judged) {
			return true
		}
	}
	return false
}

// RecordFindingProgress puts, on the performer's dispatch result, how its output
// stands against the blocking findings it was offered. It runs once per
// dispatch, at the point the output is final, after RecordFindingAnswers has
// recorded the answers. A performer offered nothing is a no-op.
func RecordFindingProgress(res *verb.DispatchResult, offered []Finding, m ProgressMaterial) {
	res.Progress = computeProgress(offered, res.Answers, m)
}

// recordFindingProgress is RecordFindingProgress over the material the output is
// judged against: artifact is the step's validated output document, nil when the
// step returned none. Nothing is read when the performer was offered no blocking
// finding.
func (g *Engine) recordFindingProgress(ctx context.Context, res *verb.DispatchResult, item workitem.Item, offered []Finding, artifact *agentartifact.Artifact) {
	if len(blockingFindingIDs(offered)) == 0 {
		return
	}
	var out *string
	name := ""
	if artifact != nil {
		out, name = &artifact.Body, artifact.Name
	}
	RecordFindingProgress(res, offered, g.progressMaterial(ctx, item, name, out))
}

// progressMaterial builds the material a performer's output is judged against:
// the story card and its documents as the gates will read them, with the step's
// output document (outputName) replaced by output when the step returned one.
func (g *Engine) progressMaterial(ctx context.Context, item workitem.Item, outputName string, output *string) ProgressMaterial {
	var docs []DocState
	if g.itemDocs != nil && item.ID != "" {
		docs = g.itemDocs(ctx, item.ID)
	}
	m := ProgressMaterial{HasOutput: output != nil}
	kept := make([]DocState, 0, len(docs)+1)
	for _, d := range docs {
		if !judgeableDoc(d) {
			continue
		}
		if output != nil && d.Name == outputName {
			m.Prior = d.Body
			continue
		}
		kept = append(kept, d)
	}
	if output != nil {
		m.Output = *output
		kept = append(kept, DocState{Name: outputName, Body: *output})
	}
	m.Judged = judgedText(item, kept)
	return m
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
