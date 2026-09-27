package reviewcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestCorpusCoverage lists the frozen corpus (t.Logf, visible under -v) and
// fails if any of the six required rubrics has no defect case or no valid
// case. It also checks the two structural invariants every case must hold:
// a non-empty change.diff, and label/expected_verdict agreement.
func TestCorpusCoverage(t *testing.T) {
	cases, err := Load(Dir())
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus is empty")
	}

	knownRubric := map[string]bool{}
	for _, r := range Rubrics {
		knownRubric[r] = true
	}

	counts := map[string]map[Label]int{}
	for _, c := range cases {
		if c.Diff == "" {
			t.Errorf("case %s (%s/%s): empty change.diff", c.ID, c.Rubric, c.Label)
		}
		if c.Label != LabelDefect && c.Label != LabelValid {
			t.Errorf("case %s: unknown label %q", c.ID, c.Label)
			continue
		}
		wantVerdict := VerdictReject
		if c.Label == LabelValid {
			wantVerdict = VerdictAccept
		}
		if c.ExpectedVerdict != wantVerdict {
			t.Errorf("case %s: label %q disagrees with expected_verdict %q (want %q)", c.ID, c.Label, c.ExpectedVerdict, wantVerdict)
		}
		if !knownRubric[c.Rubric] {
			t.Errorf("case %s: unknown rubric %q, want one of %v", c.ID, c.Rubric, Rubrics)
		}
		if counts[c.Rubric] == nil {
			counts[c.Rubric] = map[Label]int{}
		}
		counts[c.Rubric][c.Label]++
	}

	for _, gap := range coverageGaps(cases) {
		t.Errorf("%s", gap)
	}

	for _, r := range Rubrics {
		t.Logf("%-15s defect=%d valid=%d", r, counts[r][LabelDefect], counts[r][LabelValid])
	}
}

// coverageGaps reports, for the fixed Rubrics list, any rubric with zero
// defect cases or zero valid cases among the given cases.
func coverageGaps(cases []Case) []string {
	haveDefect := map[string]bool{}
	haveValid := map[string]bool{}
	for _, c := range cases {
		switch c.Label {
		case LabelDefect:
			haveDefect[c.Rubric] = true
		case LabelValid:
			haveValid[c.Rubric] = true
		}
	}
	var gaps []string
	for _, r := range Rubrics {
		if !haveDefect[r] {
			gaps = append(gaps, fmt.Sprintf("rubric %q has no defect case", r))
		}
		if !haveValid[r] {
			gaps = append(gaps, fmt.Sprintf("rubric %q has no valid case", r))
		}
	}
	return gaps
}

// TestCoverageGapsDetectsMissingSide is the negative proof for coverageGaps:
// a corpus missing one side of one rubric must be reported, not silently
// accepted. It writes a real, minimal corpus to a t.TempDir() and loads it
// through Load, so the check exercises the same path production code takes.
func TestCoverageGapsDetectsMissingSide(t *testing.T) {
	root := t.TempDir()
	for _, r := range Rubrics {
		writeCase(t, root, r, "defect-01", LabelDefect, VerdictReject)
		if r == "done" {
			continue // deliberately omit the valid side for "done"
		}
		writeCase(t, root, r, "valid-01", LabelValid, VerdictAccept)
	}

	cases, err := Load(root)
	if err != nil {
		t.Fatalf("load temp corpus: %v", err)
	}
	gaps := coverageGaps(cases)
	if len(gaps) != 1 {
		t.Fatalf("coverageGaps = %v, want exactly one gap", gaps)
	}
	want := `rubric "done" has no valid case`
	if gaps[0] != want {
		t.Fatalf("coverageGaps = %q, want %q", gaps[0], want)
	}
}

func writeCase(t *testing.T, root, rubric, id string, label Label, verdict Verdict) {
	t.Helper()
	dir := filepath.Join(root, rubric, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	c := Case{
		ID:              id,
		Rubric:          rubric,
		Skill:           "satelle-example-review",
		Label:           label,
		StoryID:         "sty_00000000",
		ExpectedVerdict: verdict,
		Summary:         "synthetic case for coverageGaps negative proof",
	}
	if label == LabelDefect {
		c.Source = &Source{
			Kind:     SourceLedgerReviewNote,
			StoryID:  "sty_00000000",
			Skill:    "satelle-example-review",
			LedgerID: "evt_00000000",
			Note:     "synthetic reject note",
		}
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatalf("marshal case.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case.json"), raw, 0o644); err != nil {
		t.Fatalf("write case.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "change.diff"), []byte("--- a/x\n+++ b/x\n@@\n-old\n+new\n"), 0o644); err != nil {
		t.Fatalf("write change.diff: %v", err)
	}
}

// TestCorpusDefectsCiteSource is AC2: every defect case's source must name a
// real-shaped story id, the rubric's mapped skill, and a non-empty note —
// otherwise the expected reject is asserted rather than cited.
func TestCorpusDefectsCiteSource(t *testing.T) {
	cases, err := Load(Dir())
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	storyID := regexp.MustCompile(`^sty_[0-9a-f]{8}$`)
	for _, c := range cases {
		if c.Label != LabelDefect {
			continue
		}
		wantSkill, ok := rubricSkill[c.Rubric]
		if !ok {
			t.Errorf("case %s: rubric %q has no mapped skill", c.ID, c.Rubric)
			continue
		}
		if c.Skill != wantSkill {
			t.Errorf("case %s: skill %q, want %q for rubric %q", c.ID, c.Skill, wantSkill, c.Rubric)
		}
		if c.Source == nil {
			t.Errorf("case %s: defect case has no source", c.ID)
			continue
		}
		if !storyID.MatchString(c.Source.StoryID) {
			t.Errorf("case %s: source.story_id %q is not a sty_ id", c.ID, c.Source.StoryID)
		}
		if c.Source.Skill != wantSkill {
			t.Errorf("case %s: source.skill %q, want %q (outside the rubric mapping)", c.ID, c.Source.Skill, wantSkill)
		}
		if c.Source.Note == "" {
			t.Errorf("case %s: source.note is empty", c.ID)
		}
		if c.Source.Kind != SourceLedgerReviewNote && c.Source.Kind != SourceCapturedNote {
			t.Errorf("case %s: source.kind %q is neither ledger_review_note nor captured_note", c.ID, c.Source.Kind)
		}
	}
}

// rubricSkill is the rubric → gate-skill mapping named in the ready-review
// premise check for this story (sty_51a04b93): the reviewer that actually
// gates each transition in this repo's step.toml.
var rubricSkill = map[string]string{
	"ready":          "satelle-story-ready-review",
	"start":          "satelle-story-plan-review",
	"integration":    "satelle-integration-review",
	"implementation": "satelle-code-ac-review",
	"coverage":       "satelle-story-integration-coverage-review",
	"done":           "satelle-story-done-review",
}
