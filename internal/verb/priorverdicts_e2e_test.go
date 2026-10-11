package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// captureRunner is an agent CLI that returns a scripted verdict per call and
// keeps every request, so a test can read exactly what rode the reviewer's stdin.
// priorFiles is the prior_verdicts file named by that request, read during Run
// before a successful review deletes the scratch directory. Empty when the
// work item omits the key.
type captureRunner struct {
	outs       []string
	reqs       []agentcli.Request
	priorFiles []string
}

func (c *captureRunner) Name() string    { return "capture" }
func (c *captureRunner) Command() string { return "capture -p --append-system-prompt {system}" }
func (c *captureRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	c.reqs = append(c.reqs, req)
	c.priorFiles = append(c.priorFiles, priorVerdictFile(req.Payload))
	i := len(c.reqs) - 1
	if i >= len(c.outs) {
		i = len(c.outs) - 1
	}
	return []byte(c.outs[i]), nil
}

// priorVerdictFile reads the file a referenced prior_verdicts path names.
// An inline array, or a missing key, yields "".
func priorVerdictFile(payload string) string {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &top) != nil {
		return ""
	}
	raw, ok := top["prior_verdicts"]
	if !ok {
		return ""
	}
	var ref struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Path == "" {
		return ""
	}
	b, err := os.ReadFile(ref.Path)
	if err != nil {
		return ""
	}
	return string(b)
}

// pvReviewSkill is a minimal reviewer rubric that passes the structure and
// verdict-contract preflights the gate runs before it dispatches.
const pvReviewSkill = `---
name: pv-plan-review
type: skill
scope: project
tags: [type:skill, type:reviewer]
description: Fixture gate on plan → in_progress for the prior-verdict payload test.
---

# Fixture plan review

Judge the plan against the story.

Return JSON {"decision": "accept"|"reject", "notes": "…"}.
`

// pvRouteWorkflow gates plan → in_progress with the fixture reviewer.
var pvRouteWorkflow = routeHalves(
	`[feature]
obligations = ["raised", "planned", "coded", "closed"]
`,
	`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
requires = ["raised"]

[coded]
status = "in_progress"
reviewers = ["pv-plan-review"]
reviewer_agent = "reviewer"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

// TestSecondGateAttemptStdinCarriesPriorVerdict (sty_0f5e600c AC4): end to end,
// with the REAL engine as the transition gater and the REAL verb.PriorVerdicts as
// its resolver — a first attempt is judged with no memory, and the SECOND
// attempt's work item names a prior_verdicts file that holds the first verdict.
// The backlog→plan verdict seeded alongside must not appear (AC2).
func TestSecondGateAttemptStdinCarriesPriorVerdict(t *testing.T) {
	withWiring(t)
	const (
		firstNotes = "PV-E2E-FIRST-VERDICT-MARKER: AC3 is unplanned"
		otherEdge  = "PV-E2E-OTHER-EDGE-MARKER"
	)
	db := wire(t)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))

	wfDir, skillDir := t.TempDir(), t.TempDir()
	writeRouteFiles(t, wfDir, pvRouteWorkflow)
	if err := os.WriteFile(filepath.Join(skillDir, "pv-plan-review.md"), []byte(pvReviewSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir, "skills": skillDir}})

	runner := &captureRunner{outs: []string{
		`{"decision":"reject","notes":"` + firstNotes + `"}`,
		`{"decision":"accept","notes":"the plan now covers AC3"}`,
	}}
	rev := agentstep.New(runner, db.DocIndex, t.TempDir(), "")
	// The same wiring internal/cli/app.go performs at both agentstep.New sites:
	// the engine's resolver IS verb.PriorVerdicts reading the ledger this
	// transition writes.
	rev.SetPriorVerdictsResolver(func(ctx context.Context, itemID, from, to string) []agentstep.PriorVerdict {
		verdicts, err := verb.PriorVerdicts(ctx, itemID, from, to)
		if err != nil {
			t.Errorf("PriorVerdicts: %v", err)
			return nil
		}
		out := make([]agentstep.PriorVerdict, 0, len(verdicts))
		for _, v := range verdicts {
			out = append(out, agentstep.PriorVerdictFrom(v))
		}
		return out
	})
	verb.SetTransitionGater(rev)

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "prior verdicts ride the payload", "category": "feature",
		"body": "b", "acceptance_criteria": "1. a",
	}), &story); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": story.ID, "status": "plan"})
	// A verdict on ANOTHER edge of the SAME story: it must never reach this edge's
	// reviewer.
	seedVerdict(t, story.ID, "review_reject", "backlog", "plan", "pv-intent-review", otherEdge)

	// Attempt 1 — the gate rejects, and verb records the verdict.
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
		t.Fatal("first attempt must be blocked by the reject")
	} else if !strings.Contains(err.Error(), firstNotes) {
		t.Fatalf("reject error should carry the verdict notes: %v", err)
	}

	// Attempt 2 — the live re-review.
	var after workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), &after); err != nil {
		t.Fatal(err)
	}
	if after.Status != "in_progress" {
		t.Fatalf("second attempt should enact: status = %q", after.Status)
	}
	if len(runner.reqs) != 2 {
		t.Fatalf("reviewer ran %d times, want 2", len(runner.reqs))
	}

	first, second := runner.reqs[0].Payload, runner.reqs[1].Payload
	if strings.Contains(first, "prior_verdicts") || runner.priorFiles[0] != "" {
		t.Errorf("first attempt at the edge must carry no prior verdicts:\n%s", first)
	}
	if !strings.Contains(second, `"prior_verdicts"`) {
		t.Fatalf("second attempt's reviewer stdin missing prior_verdicts:\n%s", second)
	}
	file := runner.priorFiles[1]
	for _, want := range []string{firstNotes, `"decision":"reject"`, `"attempt":1`, `"skill":"pv-plan-review"`} {
		if !strings.Contains(file, want) {
			t.Errorf("prior verdicts file missing %q:\n%s", want, file)
		}
	}
	if strings.Contains(file, otherEdge) || strings.Contains(second, otherEdge) {
		t.Errorf("a verdict from another edge rode this edge's payload:\n%s\nfile:\n%s", second, file)
	}
}

// newPVHarness is the prior-verdict gate fixture: a fresh store, the fixture
// reviewer on plan → in_progress, and a story sitting at plan. resolver nil
// reads the ledger through verb.PriorVerdicts and PriorVerdictFrom.
func newPVHarness(t *testing.T, outs []string, resolver func(context.Context, string, string, string) []agentstep.PriorVerdict) (*captureRunner, workitem.Item) {
	t.Helper()
	withWiring(t)
	db := wire(t)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))
	wfDir, skillDir := t.TempDir(), t.TempDir()
	writeRouteFiles(t, wfDir, pvRouteWorkflow)
	if err := os.WriteFile(filepath.Join(skillDir, "pv-plan-review.md"), []byte(pvReviewSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir, "skills": skillDir}})
	runner := &captureRunner{outs: outs}
	rev := agentstep.New(runner, db.DocIndex, t.TempDir(), "")
	if resolver == nil {
		resolver = func(ctx context.Context, itemID, from, to string) []agentstep.PriorVerdict {
			verdicts, err := verb.PriorVerdicts(ctx, itemID, from, to)
			if err != nil {
				t.Errorf("PriorVerdicts: %v", err)
				return nil
			}
			out := make([]agentstep.PriorVerdict, 0, len(verdicts))
			for _, v := range verdicts {
				out = append(out, agentstep.PriorVerdictFrom(v))
			}
			return out
		}
	}
	rev.SetPriorVerdictsResolver(resolver)
	verb.SetTransitionGater(rev)
	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "reviewed quotation", "category": "feature",
		"body": "b", "acceptance_criteria": "1. a",
	}), &story); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": story.ID, "status": "plan"})
	return runner, story
}

func verdictJSON(decision, notes, reviewed string) string {
	b, err := json.Marshal(map[string]string{
		"decision": decision, "notes": notes, "reviewed": reviewed,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func reviewPayloads(t *testing.T, storyID, kind string) []map[string]any {
	t.Helper()
	var entries []struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": storyID, "kind": kind}), &entries); err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("payload: %v (%s)", err, e.Payload)
		}
		out = append(out, p)
	}
	return out
}

func seedReviewed(t *testing.T, storyID, kind, notes, reviewed string) {
	t.Helper()
	payload := map[string]any{
		"from": "plan", "to": "in_progress", "skill": "pv-plan-review", "order": 0,
		"notes": notes, "accept": kind == ledger.KindReviewAccept,
	}
	if reviewed != "" {
		payload["reviewed"] = reviewed
	}
	call(t, "ledger-append", map[string]any{
		"story_id": storyID, "kind": kind, "actor": "reviewer",
		"body": "seeded " + kind, "payload": payload,
	})
}

// TestReviewedQuotationRoundTrip (AC1): a distinctive quotation is stored on
// the ledger row, returned by PriorVerdicts and PriorVerdictFrom, and named
// in the next presentation's prior_verdicts.json. It is not a substring of notes.
func TestReviewedQuotationRoundTrip(t *testing.T) {
	const (
		quote = "REVIEWED-QUOTATION-3d279f8b"
		notes = "premise is unproven"
	)
	if strings.Contains(notes, quote) {
		t.Fatal("fixture quotation must not be a substring of notes")
	}
	runner, story := newPVHarness(t, []string{
		verdictJSON("reject", notes, quote),
		verdictJSON("accept", "the plan now covers the premise", "later"),
	}, nil)
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
		t.Fatal("first presentation must be blocked by the reject")
	}
	rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
	if len(rows) == 0 {
		t.Fatal("no review_reject row")
	}
	last := rows[len(rows)-1]
	if last["reviewed"] != quote {
		t.Fatalf("ledger reviewed = %#v, want the quotation", last["reviewed"])
	}
	if strings.Contains(fmtNotes(last["notes"]), quote) {
		t.Fatal("quotation was stored only inside notes")
	}
	got, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("PriorVerdicts returned nothing")
	}
	conv := agentstep.PriorVerdictFrom(got[len(got)-1])
	if conv.Reviewed != quote || conv.Decision != "reject" || conv.Notes != notes || conv.ReviewedTruncated {
		t.Fatalf("PriorVerdictFrom = %+v", conv)
	}
	if conv.Attempt != 0 {
		t.Fatalf("converter invented attempt %d", conv.Attempt)
	}
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
		t.Fatal(err)
	}
	if len(runner.reqs) != 2 {
		t.Fatalf("reviewer ran %d times, want 2", len(runner.reqs))
	}
	if !strings.Contains(runner.priorFiles[1], quote) {
		t.Fatalf("prior_verdicts.json missing the quotation:\n%s", runner.priorFiles[1])
	}
}

// TestPriorVerdictStaleReachesGatePayload (sty_0225fc2f AC1): after a real reject,
// a story log event makes the next presentation's prior_verdicts carry no
// quotation, reviewed_stale and the evidence; a presentation with nothing new
// recorded keeps the quotation.
func TestPriorVerdictStaleReachesGatePayload(t *testing.T) {
	const quote = "STALE-E2E-QUOTATION"
	runner, story := newPVHarness(t, []string{
		verdictJSON("reject", "needs plan-consumed", quote),
		verdictJSON("reject", "still", quote),
		verdictJSON("accept", "ok", quote),
	}, nil)
	present := func() {
		t.Helper()
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want the reviewer's reject")
		}
	}
	present()
	// Nothing recorded since: the quotation survives so an unchanged
	// presentation can still re-issue.
	present()
	if !strings.Contains(runner.priorFiles[1], quote) || strings.Contains(runner.priorFiles[1], "reviewed_stale") {
		t.Fatalf("unchanged presentation lost its quotation:\n%s", runner.priorFiles[1])
	}
	call(t, "story-log", map[string]any{"id": story.ID, "kind": "plan-consumed"})
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
		t.Fatal(err)
	}
	var priors []agentstep.PriorVerdict
	if err := json.Unmarshal([]byte(runner.priorFiles[2]), &priors); err != nil {
		t.Fatalf("prior file: %v\n%s", err, runner.priorFiles[2])
	}
	latest := priors[len(priors)-1]
	if latest.Reviewed != "" || !latest.ReviewedStale || len(latest.EvidenceSince) != 1 ||
		latest.EvidenceSince[0].Kind != ledger.KindTelemetryEvent || latest.EvidenceSince[0].Event != "plan-consumed" {
		t.Fatalf("latest prior verdict = %+v, want stale with the plan-consumed evidence", latest)
	}
	// Only the latest verdict is withheld; older ones are left as recorded.
	if priors[0].ReviewedStale || priors[0].Reviewed != quote {
		t.Fatalf("older prior verdict = %+v, want untouched", priors[0])
	}
}

func fmtNotes(v any) string {
	s, _ := v.(string)
	return s
}

// TestCitationReissueAndChangedQuotation (AC3, AC4): the new row keeps the
// decision and the notes the reviewer wrote. Go does not compare quotations.
func TestCitationReissueAndChangedQuotation(t *testing.T) {
	const quote = "SAME-QUOTATION-FOR-REISSUE"
	t.Run("accept", func(t *testing.T) {
		_, story := newPVHarness(t, []string{verdictJSON("accept", "re-issue attempt 2", quote)}, nil)
		seedReviewed(t, story.ID, ledger.KindReviewAccept, "older accept", "older words")
		seedReviewed(t, story.ID, ledger.KindReviewAccept, "latest accept", quote)
		if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
			t.Fatal(err)
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewAccept)
		last := rows[len(rows)-1]
		if last["accept"] != true || !strings.Contains(fmtNotes(last["notes"]), "attempt 2") {
			t.Fatalf("re-issued accept = %#v", last)
		}
	})
	t.Run("reject", func(t *testing.T) {
		_, story := newPVHarness(t, []string{verdictJSON("reject", "re-issue attempt 2", quote)}, nil)
		seedReviewed(t, story.ID, ledger.KindReviewReject, "older reject", "older words")
		seedReviewed(t, story.ID, ledger.KindReviewReject, "latest reject", quote)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("an unchanged reject must stay a reject")
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
		last := rows[len(rows)-1]
		if last["accept"] != false || !strings.Contains(fmtNotes(last["notes"]), "attempt 2") {
			t.Fatalf("re-issued reject = %#v", last)
		}
	})
	t.Run("changed", func(t *testing.T) {
		const (
			newQuote = "NEW-QUOTATION-PLAN-AC3"
			notes    = "reviewed text changed: the plan's AC3"
		)
		_, story := newPVHarness(t, []string{verdictJSON("reject", notes, newQuote)}, nil)
		seedReviewed(t, story.ID, ledger.KindReviewAccept, "previous", quote)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want the reviewer's reject")
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
		if len(rows) == 0 {
			t.Fatal("no new reject row")
		}
		last := rows[len(rows)-1]
		if last["reviewed"] != newQuote || !strings.Contains(fmtNotes(last["notes"]), notes) {
			t.Fatalf("changed quotation row = %#v", last)
		}
		if last["reviewed"] == quote {
			t.Fatal("the new row kept the previous quotation")
		}
	})
}

// TestReviewedCeilingLedgerOmitsPrefix (AC5): over the ceiling the row omits
// reviewed and records reviewed_truncated. At the ceiling the full string is
// stored. A later prior-verdict record says the same. No prefix is stored.
func TestReviewedCeilingLedgerOmitsPrefix(t *testing.T) {
	t.Run("over", func(t *testing.T) {
		over := strings.Repeat("Z", 8193)
		runner, story := newPVHarness(t, []string{
			verdictJSON("reject", "too long", over),
			verdictJSON("accept", "re-judged", "short"),
		}, nil)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want reject")
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(last)
		if _, ok := last["reviewed"]; ok {
			t.Fatalf("over-ceiling row stored reviewed: %s", raw)
		}
		if last["reviewed_truncated"] != true {
			t.Fatalf("reviewed_truncated = %#v", last["reviewed_truncated"])
		}
		if strings.Contains(string(raw), strings.Repeat("Z", 32)) {
			t.Fatal("over-ceiling row stored a shortened quotation")
		}
		got, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress")
		if err != nil {
			t.Fatal(err)
		}
		conv := agentstep.PriorVerdictFrom(got[len(got)-1])
		if conv.Reviewed != "" || !conv.ReviewedTruncated {
			t.Fatalf("prior record = %+v", conv)
		}
		if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
			t.Fatal(err)
		}
		var priors []agentstep.PriorVerdict
		if err := json.Unmarshal([]byte(runner.priorFiles[1]), &priors); err != nil {
			t.Fatalf("prior file: %v\n%s", err, runner.priorFiles[1])
		}
		latest := priors[len(priors)-1]
		if latest.Reviewed != "" || !latest.ReviewedTruncated {
			t.Fatalf("prior_verdicts.json latest = %+v", latest)
		}
		if strings.Contains(runner.priorFiles[1], strings.Repeat("Z", 32)) {
			t.Fatal("prior_verdicts.json kept a prefix of the quotation")
		}
	})
	t.Run("at", func(t *testing.T) {
		at := strings.Repeat("Y", 8192)
		runner, story := newPVHarness(t, []string{
			verdictJSON("reject", "at the ceiling", at),
			verdictJSON("accept", "re-judged", "short"),
		}, nil)
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want reject")
		}
		rows := reviewPayloads(t, story.ID, ledger.KindReviewReject)
		last := rows[len(rows)-1]
		if last["reviewed"] != at {
			got, _ := last["reviewed"].(string)
			t.Fatalf("at-ceiling reviewed len = %d, want 8192", len(got))
		}
		if _, ok := last["reviewed_truncated"]; ok {
			t.Fatalf("at-ceiling flag = %#v, want it omitted", last["reviewed_truncated"])
		}
		got, err := verb.PriorVerdicts(context.Background(), story.ID, "plan", "in_progress")
		if err != nil {
			t.Fatal(err)
		}
		conv := agentstep.PriorVerdictFrom(got[len(got)-1])
		if conv.Reviewed != at || conv.ReviewedTruncated {
			t.Fatalf("prior record len %d truncated %v", len(conv.Reviewed), conv.ReviewedTruncated)
		}
		if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(runner.priorFiles[1], at) {
			t.Fatal("prior_verdicts.json dropped the at-ceiling quotation")
		}
	})
}

// TestLegacyReviewRowHasNoReviewed (AC6): a row written before reviewed
// existed still loads, and a second presentation of that edge still invokes
// the reviewer. This does not call the unchanged-reviewed invocation test.
func TestLegacyReviewRowHasNoReviewed(t *testing.T) {
	wireLedgerOnly(t)
	const notes = "LEGACY-NOTES-NO-REVIEWED"
	seedVerdict(t, "sty_legacy", ledger.KindReviewAccept, "plan", "in_progress", "pv-plan-review", notes)
	got, err := verb.PriorVerdicts(context.Background(), "sty_legacy", "plan", "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(got))
	}
	if got[0].Decision != "accept" || got[0].Notes != notes || got[0].Reviewed != "" || got[0].ReviewedTruncated {
		t.Fatalf("legacy prior = %+v", got[0])
	}
	conv := agentstep.PriorVerdictFrom(got[0])
	if conv.Decision != "accept" || conv.Notes != notes || conv.Reviewed != "" || conv.ReviewedTruncated || conv.Attempt != 0 {
		t.Fatalf("PriorVerdictFrom = %+v", conv)
	}

	runner, story := newPVHarness(t, []string{`{"decision":"accept","notes":"re-judged"}`}, func(context.Context, string, string, string) []agentstep.PriorVerdict {
		return []agentstep.PriorVerdict{conv}
	})
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}), new(workitem.Item)); err != nil {
		t.Fatal(err)
	}
	if len(runner.reqs) != 1 {
		t.Fatalf("legacy row must still invoke the reviewer, ran %d times", len(runner.reqs))
	}
}
