// Package reviewcorpus is the frozen corpus of known-defect and known-valid
// changes named by epic sty_1e037210's close criterion: a fixture later
// reviewer-process changes are judged against, so a proof rubric's coverage
// never has to be re-argued from scratch. The bundling (sty_23e10d92),
// isolation (sty_9ded2605), audit (sty_9ed88e1e) and warm-resume
// (sty_91d44f06) stories load it through Dir and Load rather than inventing
// another set.
//
// The case model, the loader and the verdict classification are owned by
// internal/reviewscore (sty_29741ad6), which also scores a reviewer binding over
// this corpus; this package re-exports them so its consumers keep one import and
// the fixture stays the only thing that lives under tests/.
package reviewcorpus

import (
	"path/filepath"
	"runtime"

	"github.com/bobmcallan/satelle/internal/reviewscore"
)

// Label states whether a case demonstrates a known reviewer defect (an
// expected reject) or a known-valid change (an expected accept).
type Label = reviewscore.Label

const (
	LabelDefect = reviewscore.LabelDefect
	LabelValid  = reviewscore.LabelValid
)

// Verdict is the reviewer decision a case expects.
type Verdict = reviewscore.Verdict

const (
	VerdictReject = reviewscore.VerdictReject
	VerdictAccept = reviewscore.VerdictAccept
)

// SourceKind names where a defect case's cited note comes from.
type SourceKind = reviewscore.SourceKind

const (
	SourceLedgerReviewNote = reviewscore.SourceLedgerReviewNote
	SourceCapturedNote     = reviewscore.SourceCapturedNote
)

// Source cites the recorded evidence a defect case's expected reject comes from.
type Source = reviewscore.Source

// Case is one frozen corpus entry (see reviewscore.Case).
type Case = reviewscore.Case

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
func Load(root string) ([]Case, error) { return reviewscore.Load(root) }
