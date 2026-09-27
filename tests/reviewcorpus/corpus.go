// Package reviewcorpus is the frozen corpus of known-defect and known-valid
// changes named by epic sty_1e037210's close criterion: a fixture later
// reviewer-process changes are judged against, so a proof rubric's coverage
// never has to be re-argued from scratch. The bundling (sty_23e10d92),
// isolation (sty_9ded2605), audit (sty_9ed88e1e) and warm-resume
// (sty_91d44f06) stories load it through Dir and Load rather than inventing
// another set.
package reviewcorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
)

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

// Case is one frozen corpus entry: a rubric, a label, the diff under review
// and, for a defect, the recorded note its expected reject cites.
type Case struct {
	ID              string  `json:"id"`
	Rubric          string  `json:"rubric"`
	Skill           string  `json:"skill"`
	Label           Label   `json:"label"`
	StoryID         string  `json:"story_id"`
	ExpectedVerdict Verdict `json:"expected_verdict"`
	Source          *Source `json:"source,omitempty"`
	Summary         string  `json:"summary"`

	// Dir and Diff are populated by Load, not decoded from case.json.
	Dir  string `json:"-"`
	Diff string `json:"-"`
}

// Rubrics is the fixed set of proof rubrics the corpus must cover (AC3): a
// const list, not derived from the data on disk, so a missing rubric
// directory is caught rather than silently narrowing coverage.
var Rubrics = []string{"ready", "start", "integration", "implementation", "coverage", "done"}

// Dir is the corpus root under this package's testdata, resolved relative to
// this source file so a consumer outside tests/reviewcorpus can still find it
// without guessing a working directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "corpus")
}

// Load reads every case directory under root (see Dir), sorted by rubric
// then case id.
func Load(root string) ([]Case, error) {
	rubricEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read corpus root %s: %w", root, err)
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
			c, err := loadCase(filepath.Join(rubricDir, ce.Name()))
			if err != nil {
				return nil, err
			}
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

// loadCase decodes one case.json with unknown fields rejected, so a typo in
// the corpus fails loudly instead of silently dropping a field, and pairs it
// with its frozen change.diff.
func loadCase(dir string) (Case, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		return Case{}, fmt.Errorf("read %s/case.json: %w", dir, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Case
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf("decode %s/case.json: %w", dir, err)
	}
	diff, err := os.ReadFile(filepath.Join(dir, "change.diff"))
	if err != nil {
		return Case{}, fmt.Errorf("read %s/change.diff: %w", dir, err)
	}
	c.Dir = dir
	c.Diff = string(diff)
	return c, nil
}
