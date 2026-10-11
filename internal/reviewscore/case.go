// Package reviewscore scores a reviewer binding on known cases: the frozen
// review corpus (tests/reviewcorpus) and replay cases captured from recorded
// gate rows. It owns the case model and the verdict classification, so there is
// one definition of "a missed defect" and "a false rejection" for the bundling
// parity run and for the per-binding score alike; tests/reviewcorpus re-exports
// both.
//
// The package measures review quality, not rejection counts: a case is a known
// defect (an expected reject) or a known-valid change (an expected accept), and
// a binding is scored by what it did with each.
package reviewscore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bobmcallan/satelle/internal/verb"
)

// Label states whether a case demonstrates a known reviewer defect (an
// expected reject) or a known-valid change (an expected accept).
type Label string

const (
	LabelDefect Label = "defect"
	LabelValid  Label = "valid"
)

// Verdict is the reviewer decision a case expects.
type Verdict string

const (
	VerdictReject Verdict = "reject"
	VerdictAccept Verdict = "accept"
)

// SourceKind names where a defect case's cited note comes from.
type SourceKind string

const (
	SourceLedgerReviewNote SourceKind = "ledger_review_note"
	SourceCapturedNote     SourceKind = "captured_note"
	// SourceLedgerReplay marks a replay case captured from one recorded review
	// row (`satelle review capture`).
	SourceLedgerReplay SourceKind = "ledger_replay"
)

// PayloadReconstructed is the PayloadSource of every replay case: the ledger
// keeps only the size of the payload a reviewer saw, never the payload, so the
// replay payload is rebuilt from the row, the definition edits and the
// documents, and is not the original bytes.
const PayloadReconstructed = "reconstructed"

// Source cites the recorded evidence a defect case's expected reject comes
// from (AC2): a real story id, the skill that rejected it, and the note text
// itself, so the case is traceable rather than asserted.
type Source struct {
	Kind     SourceKind `json:"kind"`
	StoryID  string     `json:"story_id"`
	Skill    string     `json:"skill"`
	LedgerID string     `json:"ledger_id,omitempty"`
	Note     string     `json:"note"`
}

// Case is one known case: a rubric, a label, what the reviewer is shown and,
// for a defect, the recorded note its expected reject cites. A corpus case is
// shown a frozen change.diff; a replay case is shown the reconstructed story
// definition and documents in Payload.
type Case struct {
	ID              string  `json:"id"`
	Rubric          string  `json:"rubric"`
	Skill           string  `json:"skill"`
	Label           Label   `json:"label"`
	StoryID         string  `json:"story_id"`
	ExpectedVerdict Verdict `json:"expected_verdict"`
	Source          *Source `json:"source,omitempty"`
	Summary         string  `json:"summary"`

	// DefectMarkers are the phrases a reviewer's notes cite when it names this
	// case's known defect; empty means finding match is not scored for the case.
	DefectMarkers []string `json:"defect_markers,omitempty"`

	// From/To/OriginalNotes/PayloadSource describe a replay case: the edge the
	// recorded row judged, the reviewer's original notes, and how the payload
	// was obtained (always PayloadReconstructed).
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	OriginalNotes string `json:"original_notes,omitempty"`
	PayloadSource string `json:"payload_source,omitempty"`
	// PayloadGaps name what the replay payload lacks of what the recorded
	// reviewer judged (the reviewed patch, a document as it stood then). A case
	// with gaps is unfaithful: a binding's verdict on it says nothing about the
	// binding, so scoring skips it and reports it rather than counting an
	// "escape" the capture manufactured.
	PayloadGaps []string `json:"payload_gaps,omitempty"`

	// Dir, Diff, Payload, Origin and Digest are populated by the loaders, not
	// decoded from case.json.
	Dir     string         `json:"-"`
	Diff    string         `json:"-"`
	Payload *ReplayPayload `json:"-"`
	Origin  Origin         `json:"-"`
	Digest  string         `json:"-"`
}

// Unfaithful reports whether a replay case lacks material its recorded reviewer
// judged. Corpus cases are frozen whole and never are.
func (c Case) Unfaithful() bool { return c.Origin == OriginReplay && len(c.PayloadGaps) > 0 }

// Origin says which loader produced a case.
type Origin string

const (
	OriginCorpus Origin = "corpus"
	OriginReplay Origin = "replay"
)

// ReplayPayload is payload.json: the story definition as it stood at the
// review, the documents attached to the story and the change under review.
// Patch is the change the reviewer was shown; NoPatch states the edge judged no
// code, so there was none to show.
type ReplayPayload struct {
	Patch   string `json:"patch,omitempty"`
	NoPatch bool   `json:"no_patch,omitempty"`
	// PriorVerdicts are the verdicts already recorded on the edge, and
	// DefinitionEdits the edits recorded on the story, when the review ran: a
	// gate injects both, so a re-review judges the delta, not the story afresh.
	PriorVerdicts      []verb.PriorVerdict   `json:"prior_verdicts,omitempty"`
	DefinitionEdits    []verb.DefinitionEdit `json:"definition_edits,omitempty"`
	Title              string                `json:"title"`
	Body               string                `json:"body,omitempty"`
	AcceptanceCriteria string                `json:"acceptance_criteria,omitempty"`
	Category           string                `json:"category,omitempty"`
	Tags               []string              `json:"tags,omitempty"`
	Docs               []ReplayDoc           `json:"docs,omitempty"`
}

// ReplayDoc is one attached document of a replay payload.
type ReplayDoc struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Body string `json:"body"`
}

// Load reads every corpus case directory under root
// (<root>/<rubric>/<case>/{case.json,change.diff}), sorted by rubric then id.
func Load(root string) ([]Case, error) {
	return loadTree(root, OriginCorpus, loadCase)
}

// LoadReplay reads every replay case under dir
// (<dir>/<rubric>/<case>/{case.json,payload.json}), sorted by rubric then id.
func LoadReplay(dir string) ([]Case, error) {
	return loadTree(dir, OriginReplay, loadReplayCase)
}

func loadTree(root string, origin Origin, one func(dir string) (Case, error)) ([]Case, error) {
	rubricEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s root %s: %w", origin, root, err)
	}
	var cases []Case
	for _, re := range rubricEntries {
		if !re.IsDir() {
			continue
		}
		rubricDir := filepath.Join(root, re.Name())
		caseEntries, err := os.ReadDir(rubricDir)
		if err != nil {
			return nil, fmt.Errorf("read rubric dir %s: %w", rubricDir, err)
		}
		for _, ce := range caseEntries {
			if !ce.IsDir() {
				continue
			}
			c, err := one(filepath.Join(rubricDir, ce.Name()))
			if err != nil {
				return nil, err
			}
			c.Origin = origin
			cases = append(cases, c)
		}
	}
	sort.Slice(cases, func(i, j int) bool {
		if cases[i].Rubric != cases[j].Rubric {
			return cases[i].Rubric < cases[j].Rubric
		}
		return cases[i].ID < cases[j].ID
	})
	return cases, nil
}

// decodeCase decodes one case.json with unknown fields rejected, so a typo in
// the fixture fails loudly instead of silently dropping a field.
func decodeCase(dir string) (Case, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		return Case{}, nil, fmt.Errorf("read %s/case.json: %w", dir, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Case
	if err := dec.Decode(&c); err != nil {
		return Case{}, nil, fmt.Errorf("decode %s/case.json: %w", dir, err)
	}
	return c, raw, nil
}

// loadCase pairs a corpus case.json with its frozen change.diff.
func loadCase(dir string) (Case, error) {
	c, raw, err := decodeCase(dir)
	if err != nil {
		return Case{}, err
	}
	diff, err := os.ReadFile(filepath.Join(dir, "change.diff"))
	if err != nil {
		return Case{}, fmt.Errorf("read %s/change.diff: %w", dir, err)
	}
	c.Dir = dir
	c.Diff = string(diff)
	c.Digest = digest(raw, diff)
	return c, nil
}

// loadReplayCase pairs a replay case.json with its payload.json.
func loadReplayCase(dir string) (Case, error) {
	c, raw, err := decodeCase(dir)
	if err != nil {
		return Case{}, err
	}
	pb, err := os.ReadFile(filepath.Join(dir, "payload.json"))
	if err != nil {
		return Case{}, fmt.Errorf("read %s/payload.json: %w", dir, err)
	}
	dec := json.NewDecoder(bytes.NewReader(pb))
	dec.DisallowUnknownFields()
	var p ReplayPayload
	if err := dec.Decode(&p); err != nil {
		return Case{}, fmt.Errorf("decode %s/payload.json: %w", dir, err)
	}
	c.Dir = dir
	c.Payload = &p
	c.Digest = digest(raw, pb)
	return c, nil
}

func digest(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:", len(p))
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
