package reviewscore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// StoryDefinition is the part of a story a reviewer judges: the words of its
// definition. It is read at capture time, so Capture winds recorded edits back.
type StoryDefinition struct {
	ID                 string
	Title              string
	Body               string
	AcceptanceCriteria string
	Category           string
	Tags               []string
}

// CaptureInput is everything `satelle review capture` reads from the store; the
// command gathers it through the verbs and Capture itself touches no store.
type CaptureInput struct {
	Story StoryDefinition
	// Entries are the story's ledger rows, oldest first.
	Entries  []ledger.Entry
	LedgerID string
	// Expect is the human-judged verdict for the recorded edge — independent of
	// what the recorded reviewer said.
	Expect  Verdict
	Markers []string
	// Rubric groups the case in reports; it defaults to the reviewer skill.
	Rubric string
	// Docs are the story's attached documents at capture time.
	Docs []ReplayDoc
	// Patch is the change the recorded reviewer was shown; NoPatch states the
	// edge judged no code. The ledger keeps neither, so Capture requires one:
	// a replay with no change to judge would score a missing diff as an escape.
	Patch   string
	NoPatch bool
	// ReviewedDocs are documents as they stood at the review, supplied by the
	// operator. They replace the captured document of the same name, and stand in
	// for one that has since been removed.
	ReviewedDocs []ReplayDoc
	OutDir       string
}

type reviewRowPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Attempt string `json:"attempt"`
	Skill   string `json:"skill"`
	Notes   string `json:"notes"`
}

// before returns the story's rows a gate had on hand when it presented the review
// row at idx: everything recorded earlier, less the other verdicts of the same
// presentation, which a gate writes only after every reviewer has answered.
func before(entries []ledger.Entry, idx int, attempt string) []ledger.Entry {
	var out []ledger.Entry
	for _, e := range entries[:idx] {
		if attempt != "" && (e.Kind == ledger.KindReviewAccept || e.Kind == ledger.KindReviewReject) {
			var p reviewRowPayload
			if json.Unmarshal(e.Payload, &p) == nil && p.Attempt == attempt {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

type definitionEditRow struct {
	Field string  `json:"field"`
	Old   *string `json:"old"`
}

// Capture turns one recorded review row into a replay case under
// <OutDir>/<rubric>/<case-id>/ and returns that directory. The ledger keeps the
// size of the payload a reviewer saw but not the payload, so the case is
// reconstructed from the row, the definition edits recorded after it and the
// documents, and is marked PayloadReconstructed.
func Capture(in CaptureInput) (string, error) {
	if in.Expect != VerdictAccept && in.Expect != VerdictReject {
		return "", fmt.Errorf("--expect must be accept or reject, got %q", in.Expect)
	}
	if strings.TrimSpace(in.OutDir) == "" {
		return "", fmt.Errorf("--out is required")
	}
	idx := -1
	for i, e := range in.Entries {
		if e.ID == in.LedgerID {
			idx = i
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("ledger row %s is not on story %s", in.LedgerID, in.Story.ID)
	}
	row := in.Entries[idx]
	if row.StoryID != in.Story.ID {
		return "", fmt.Errorf("ledger row %s belongs to story %q, not %s", in.LedgerID, row.StoryID, in.Story.ID)
	}
	if row.Kind != ledger.KindReviewAccept && row.Kind != ledger.KindReviewReject {
		return "", fmt.Errorf("ledger row %s is a %s row: capture needs a %s or %s row", in.LedgerID, row.Kind, ledger.KindReviewAccept, ledger.KindReviewReject)
	}
	var rp reviewRowPayload
	if err := json.Unmarshal(row.Payload, &rp); err != nil || rp.Skill == "" {
		return "", fmt.Errorf("ledger row %s carries no reviewer skill to replay", in.LedgerID)
	}

	switch {
	case in.Patch != "" && in.NoPatch:
		return "", fmt.Errorf("--patch and --no-patch are exclusive: the edge either judged a change or it did not")
	case in.Patch == "" && !in.NoPatch:
		return "", fmt.Errorf("the ledger keeps only the size of the payload the reviewer saw, not the change in it: give the reviewed change with --patch <file>, or --no-patch when the edge judged no code")
	}

	had := before(in.Entries, idx, rp.Attempt)
	def, fallback := definitionAt(in.Story, in.Entries[idx+1:])
	label := LabelValid
	if in.Expect == VerdictReject {
		label = LabelDefect
	}
	rubric := in.Rubric
	if rubric == "" {
		rubric = rp.Skill
	}
	id := in.Story.ID + "-" + in.LedgerID
	docs, gaps := docsAtReview(in.Docs, in.ReviewedDocs, in.Entries, row)
	for _, f := range fallback {
		gaps = append(gaps, fmt.Sprintf("%s has no prior value on its edit row, so today's text stands in for the one reviewed", f))
	}
	summary := fmt.Sprintf("Replay of %s (%s on %s→%s of %s); the recorded reviewer's verdict was %s, the human-judged expectation is %s. Payload %s: the ledger keeps only the payload size, so the definition and documents are rebuilt and the change is the one supplied at capture.",
		in.LedgerID, rp.Skill, rp.From, rp.To, in.Story.ID, strings.TrimPrefix(row.Kind, "review_"), in.Expect, PayloadReconstructed)
	if len(gaps) > 0 {
		summary += " UNFAITHFUL, not scored: " + strings.Join(gaps, "; ") + "."
	}
	c := Case{
		ID: id, Rubric: rubric, Skill: rp.Skill, Label: label, StoryID: in.Story.ID,
		ExpectedVerdict: in.Expect, Summary: summary, DefectMarkers: in.Markers,
		From: rp.From, To: rp.To, OriginalNotes: rp.Notes, PayloadSource: PayloadReconstructed, PayloadGaps: gaps,
		Source: &Source{Kind: SourceLedgerReplay, StoryID: in.Story.ID, Skill: rp.Skill, LedgerID: in.LedgerID, Note: rp.Notes},
	}
	payload := ReplayPayload{
		Patch: in.Patch, NoPatch: in.NoPatch,
		PriorVerdicts:   verb.PriorVerdictsFrom(had, rp.From, rp.To),
		DefinitionEdits: verb.DefinitionEditsFrom(had),
		Title:           def.Title, Body: def.Body, AcceptanceCriteria: def.AcceptanceCriteria,
		Category: def.Category, Tags: def.Tags, Docs: docs,
	}
	dir := filepath.Join(in.OutDir, rubric, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for name, v := range map[string]any{"case.json": c, "payload.json": payload} {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// CaseGaps reads the payload gaps of the replay case written at dir.
func CaseGaps(dir string) []string {
	c, _, err := decodeCase(dir)
	if err != nil {
		return nil
	}
	return c.PayloadGaps
}

// docsAtReview returns the documents the recorded reviewer saw and the gaps in
// them. Attached documents are overwritten in place, so a captured document is
// the reviewed one only if no attach row for its name follows the review row: one
// first attached after the review did not exist then and is dropped; one
// re-attached after it is today's version and is a gap unless the operator
// supplied the reviewed body; one with no attach row at all cannot be dated.
func docsAtReview(current, reviewed []ReplayDoc, entries []ledger.Entry, review ledger.Entry) ([]ReplayDoc, []string) {
	type span struct{ first, last time.Time }
	attach := map[string]span{}
	for _, e := range entries {
		if e.Kind != verb.KindStoryDocAttached {
			continue
		}
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(e.Payload, &p) != nil || p.Name == "" {
			continue
		}
		s := attach[p.Name]
		if s.first.IsZero() || e.CreatedAt.Before(s.first) {
			s.first = e.CreatedAt
		}
		if e.CreatedAt.After(s.last) {
			s.last = e.CreatedAt
		}
		attach[p.Name] = s
	}
	supplied := map[string]ReplayDoc{}
	for _, d := range reviewed {
		supplied[d.Name] = d
	}
	var docs []ReplayDoc
	var gaps []string
	for _, d := range current {
		if r, ok := supplied[d.Name]; ok {
			docs = append(docs, r)
			delete(supplied, d.Name)
			continue
		}
		s, dated := attach[d.Name]
		switch {
		case !dated:
			gaps = append(gaps, fmt.Sprintf("document %q has no attach record, so whether it changed since the review is unknown; supply the reviewed body with --doc %s=<file>", d.Name, d.Name))
		case s.first.After(review.CreatedAt):
			continue
		case s.last.After(review.CreatedAt):
			gaps = append(gaps, fmt.Sprintf("document %q was re-attached after the review, so today's version stands in for the one reviewed; supply the reviewed body with --doc %s=<file>", d.Name, d.Name))
		}
		docs = append(docs, d)
	}
	var extra []string
	for name := range supplied {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		docs = append(docs, supplied[name])
	}
	return docs, gaps
}

// definitionAt winds the story's current definition back to what it was when a
// review row was written, using the definition_edited rows recorded after it
// (after is the story's rows following that review row, oldest first). The
// earliest edit of a field after the review holds the value the field had then.
// fallback names fields whose edit row carries no prior value; those keep their
// current text.
func definitionAt(cur StoryDefinition, after []ledger.Entry) (StoryDefinition, []string) {
	def := cur
	var fallback []string
	seen := map[string]bool{}
	for _, e := range after {
		if e.Kind != ledger.KindDefinitionEdited {
			continue
		}
		var p definitionEditRow
		if json.Unmarshal(e.Payload, &p) != nil || seen[p.Field] {
			continue
		}
		seen[p.Field] = true
		if p.Old == nil {
			fallback = append(fallback, p.Field)
			continue
		}
		switch p.Field {
		case "title":
			def.Title = *p.Old
		case "body":
			def.Body = *p.Old
		case "acceptance_criteria":
			def.AcceptanceCriteria = *p.Old
		case "category":
			def.Category = *p.Old
		}
	}
	return def, fallback
}
