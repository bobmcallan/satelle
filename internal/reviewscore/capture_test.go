package reviewscore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// clock hands out strictly increasing row times, so "after the review" is exact.
var clock = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func row(id, story, kind string, payload any) ledger.Entry {
	b, _ := json.Marshal(payload)
	clock = clock.Add(time.Minute)
	return ledger.Entry{ID: id, StoryID: story, Kind: kind, Payload: b, CreatedAt: clock}
}

func attached(id, name string) ledger.Entry {
	return row(id, "sty_cap", verb.KindStoryDocAttached, map[string]string{"name": name, "type": "plan"})
}

const reviewedPatch = "--- a/x\n+++ b/x\n+REVIEWED-PATCH\n"

// captureFixture is a story with a plan attached before a review_reject row and
// later definition edits, so the definition at the review differs from today's.
// Its payload is faithful: a patch is supplied and the plan predates the review.
func captureFixture(t *testing.T, out string, expect Verdict, markers []string) CaptureInput {
	t.Helper()
	return CaptureInput{
		Story: StoryDefinition{
			ID: "sty_cap", Title: "Title now", Body: "Body now", AcceptanceCriteria: "1. AC after the edit", Category: "feature",
			Tags: []string{"epic:x"},
		},
		Entries: []ledger.Entry{
			row("evt_before", "sty_cap", ledger.KindStatusTransition, map[string]any{"from": "backlog", "to": "plan"}),
			attached("evt_att", "plan"),
			row("evt_rej", "sty_cap", ledger.KindReviewReject, map[string]any{
				"from": "backlog", "to": "plan", "skill": "satelle-story-intent-review", "notes": "no acceptance criteria exist", "accept": false,
			}),
			row("evt_edit1", "sty_cap", ledger.KindDefinitionEdited, map[string]any{"field": "acceptance_criteria", "old": "", "new": "1. first"}),
			row("evt_edit2", "sty_cap", ledger.KindDefinitionEdited, map[string]any{"field": "acceptance_criteria", "old": "1. first", "new": "1. AC after the edit"}),
			row("evt_edit3", "sty_cap", ledger.KindDefinitionEdited, map[string]any{"field": "title", "old": "Title then", "new": "Title now"}),
		},
		LedgerID: "evt_rej", Expect: expect, Markers: markers, OutDir: out,
		Patch: reviewedPatch,
		Docs:  []ReplayDoc{{Name: "plan", Type: "plan", Body: "# plan"}},
	}
}

func TestCaptureTurnsAReviewRowIntoAReconstructedReplayCase(t *testing.T) {
	out := t.TempDir()
	dir, err := Capture(captureFixture(t, out, VerdictReject, []string{"no acceptance"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(out, "satelle-story-intent-review", "sty_cap-evt_rej"); dir != want {
		t.Errorf("dir = %s, want %s", dir, want)
	}
	cases, err := LoadReplay(out)
	if err != nil || len(cases) != 1 {
		t.Fatalf("LoadReplay = %v, %v", cases, err)
	}
	c := cases[0]
	if c.ExpectedVerdict != VerdictReject || c.Label != LabelDefect || c.Skill != "satelle-story-intent-review" ||
		c.From != "backlog" || c.To != "plan" || c.OriginalNotes != "no acceptance criteria exist" {
		t.Errorf("case = %+v", c)
	}
	if c.PayloadSource != PayloadReconstructed || c.Source == nil || c.Source.Kind != SourceLedgerReplay || c.Source.LedgerID != "evt_rej" {
		t.Errorf("provenance = %+v / %+v", c.PayloadSource, c.Source)
	}
	if len(c.DefectMarkers) != 1 || c.DefectMarkers[0] != "no acceptance" {
		t.Errorf("markers = %v", c.DefectMarkers)
	}
	p := c.Payload
	if p == nil {
		t.Fatal("no payload")
	}
	// The definition as it stood at the review: the ACs did not exist yet and
	// the title was the earlier one; the body was never edited.
	if p.AcceptanceCriteria != "" || p.Title != "Title then" || p.Body != "Body now" {
		t.Errorf("definition at review = %+v", p)
	}
	if len(p.Docs) != 1 || p.Docs[0].Name != "plan" {
		t.Errorf("docs = %+v", p.Docs)
	}
	if p.Patch != reviewedPatch || p.NoPatch {
		t.Errorf("the reviewed patch must ride in the case, got %q (no_patch=%v)", p.Patch, p.NoPatch)
	}
	if c.Unfaithful() || len(c.PayloadGaps) != 0 {
		t.Errorf("a case with the reviewed patch and a plan older than the review is faithful, gaps = %v", c.PayloadGaps)
	}
}

func TestCaptureAcceptRowWithHumanExpectationIsIndependentOfTheRecordedVerdict(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictAccept, nil)
	if _, err := Capture(in); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadReplay(in.OutDir)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded reviewer rejected; the human judged the edge should pass.
	if cases[0].ExpectedVerdict != VerdictAccept || cases[0].Label != LabelValid {
		t.Errorf("case = %+v", cases[0])
	}
}

func TestCaptureRefusesWhatIsNotAReviewRowOfThatStory(t *testing.T) {
	cases := map[string]func(in *CaptureInput){
		"a non-review row":    func(in *CaptureInput) { in.LedgerID = "evt_before" },
		"an unknown row":      func(in *CaptureInput) { in.LedgerID = "evt_nope" },
		"another story's row": func(in *CaptureInput) { in.Entries[2].StoryID = "sty_other" },
		"a bad expectation":   func(in *CaptureInput) { in.Expect = "maybe" },
		"a row with no skill": func(in *CaptureInput) { in.Entries[2].Payload = json.RawMessage(`{"from":"a","to":"b"}`) },
		"no output directory": func(in *CaptureInput) { in.OutDir = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := captureFixture(t, t.TempDir(), VerdictReject, nil)
			mutate(&in)
			if _, err := Capture(in); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

// The ledger keeps only the payload size, so a replay with neither the reviewed
// patch nor a statement that the edge judged no code would have the reviewer
// judge a change that is not there — and score its accept as an escape.
func TestCaptureRequiresTheReviewedPatchOrAStatementThereWasNone(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	in.Patch = ""
	if _, err := Capture(in); err == nil || !strings.Contains(err.Error(), "--patch") {
		t.Fatalf("a capture with no patch and no --no-patch must be refused naming --patch, got %v", err)
	}
	in.NoPatch = true
	dir, err := Capture(in)
	if err != nil {
		t.Fatalf("--no-patch states the edge judged no code: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "payload.json"))
	if !strings.Contains(string(raw), `"no_patch": true`) || strings.Contains(string(raw), `"patch":`) {
		t.Errorf("payload.json must say there was no patch:\n%s", raw)
	}
	in.Patch = reviewedPatch
	if _, err := Capture(in); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("--patch with --no-patch contradict each other, got %v", err)
	}
}

func TestCaptureFlagsADocumentOverwrittenSinceTheReviewAsUnfaithful(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	in.Entries = append(in.Entries, attached("evt_reatt", "plan"))
	dir, err := Capture(in)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := LoadReplay(in.OutDir)
	if err != nil || len(cases) != 1 {
		t.Fatalf("LoadReplay = %v, %v (%s)", cases, err, dir)
	}
	c := cases[0]
	if !c.Unfaithful() || len(c.PayloadGaps) != 1 || !strings.Contains(c.PayloadGaps[0], `"plan" was re-attached after the review`) {
		t.Fatalf("gaps = %v", c.PayloadGaps)
	}
	if !strings.Contains(c.Summary, "UNFAITHFUL") {
		t.Errorf("the summary must say so: %s", c.Summary)
	}
}

func TestCaptureTakesTheReviewedBodyOfAnOverwrittenDocument(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	in.Entries = append(in.Entries, attached("evt_reatt", "plan"))
	in.ReviewedDocs = []ReplayDoc{{Name: "plan", Type: "plan", Body: "# the plan as reviewed"}, {Name: "removed", Type: "plan", Body: "# since deleted"}}
	if _, err := Capture(in); err != nil {
		t.Fatal(err)
	}
	cases, _ := LoadReplay(in.OutDir)
	c := cases[0]
	if c.Unfaithful() {
		t.Fatalf("a supplied reviewed body closes the gap, got %v", c.PayloadGaps)
	}
	var got []string
	for _, d := range c.Payload.Docs {
		got = append(got, d.Name+"="+d.Body)
	}
	if strings.Join(got, "|") != "plan=# the plan as reviewed|removed=# since deleted" {
		t.Errorf("docs = %v", got)
	}
}

func TestCaptureDropsADocumentThatDidNotExistAtTheReviewAndFlagsOneItCannotDate(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	in.Entries = append(in.Entries, attached("evt_late", "step-summary"))
	in.Docs = append(in.Docs, ReplayDoc{Name: "step-summary", Type: "summary", Body: "after"}, ReplayDoc{Name: "undated", Type: "plan", Body: "?"})
	if _, err := Capture(in); err != nil {
		t.Fatal(err)
	}
	cases, _ := LoadReplay(in.OutDir)
	c := cases[0]
	for _, d := range c.Payload.Docs {
		if d.Name == "step-summary" {
			t.Errorf("a document first attached after the review was not there for the reviewer: %+v", c.Payload.Docs)
		}
	}
	if len(c.PayloadGaps) != 1 || !strings.Contains(c.PayloadGaps[0], `"undated" has no attach record`) {
		t.Errorf("gaps = %v", c.PayloadGaps)
	}
}

// A gate injects the verdicts already on the edge and the story's definition
// edits, so a re-review judges the delta; the replay carries what it had then —
// not the other verdicts of its own presentation, nor anything recorded after.
func TestCaptureCarriesTheVerdictsAndEditsThatPrecededTheReview(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	sibling := row("evt_sib", "sty_cap", ledger.KindReviewAccept, map[string]any{
		"from": "backlog", "to": "plan", "attempt": "att_2", "skill": "satelle-other-review", "notes": "same presentation",
	})
	earlier := row("evt_prior", "sty_cap", ledger.KindReviewReject, map[string]any{
		"from": "backlog", "to": "plan", "attempt": "att_1", "skill": "satelle-story-intent-review", "notes": "first attempt: no ACs",
	})
	otherEdge := row("evt_other", "sty_cap", ledger.KindReviewReject, map[string]any{
		"from": "plan", "to": "in_progress", "attempt": "att_x", "skill": "satelle-story-plan-review", "notes": "another edge",
	})
	edit := row("evt_early_edit", "sty_cap", ledger.KindDefinitionEdited, map[string]any{"field": "body", "old": "b0", "new": "Body now", "actor": "planner"})
	rej := row("evt_rej", "sty_cap", ledger.KindReviewReject, map[string]any{
		"from": "backlog", "to": "plan", "attempt": "att_2", "skill": "satelle-story-intent-review", "notes": "still no ACs",
	})
	in.Entries = append(append(append([]ledger.Entry{}, in.Entries[:2]...), earlier, otherEdge, edit, sibling, rej), in.Entries[3:]...)
	if _, err := Capture(in); err != nil {
		t.Fatal(err)
	}
	cases, _ := LoadReplay(in.OutDir)
	p := cases[0].Payload
	if len(p.PriorVerdicts) != 1 || p.PriorVerdicts[0].Notes != "first attempt: no ACs" || p.PriorVerdicts[0].Decision != "reject" {
		t.Errorf("prior verdicts = %+v", p.PriorVerdicts)
	}
	if len(p.DefinitionEdits) != 1 || p.DefinitionEdits[0].Field != "body" || p.DefinitionEdits[0].Actor != "planner" {
		t.Errorf("definition edits = %+v", p.DefinitionEdits)
	}
	if cases[0].Unfaithful() {
		t.Errorf("gaps = %v", cases[0].PayloadGaps)
	}
}

func TestCaptureNamesAFieldWhoseEditRowHasNoPriorValue(t *testing.T) {
	in := captureFixture(t, t.TempDir(), VerdictReject, nil)
	in.Entries[5] = row("evt_edit3", "sty_cap", ledger.KindDefinitionEdited, map[string]any{"field": "title", "new": "Title now"})
	dir, err := Capture(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "case.json"))
	if !strings.Contains(string(raw), "title has no prior value") {
		t.Errorf("the fallback must be recorded in the case:\n%s", raw)
	}
	cases, _ := LoadReplay(in.OutDir)
	if !cases[0].Unfaithful() {
		t.Error("a definition field taken from today is not what the reviewer judged: the case is unfaithful")
	}
}
