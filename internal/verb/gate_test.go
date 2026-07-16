package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

type summariserStub struct {
	out   string
	calls int
}

func (s *summariserStub) Summarise(context.Context, workitem.Item, string, string) (verb.SummaryResult, error) {
	s.calls++
	return verb.SummaryResult{Text: s.out}, nil
}
func (s *summariserStub) MandatorySummary(context.Context, workitem.Item) bool { return false }

// edgeSummariser returns a summary naming the edge, so a chain of transitions
// deposits distinguishable step-summary docs.
type edgeSummariser struct{}

func (edgeSummariser) Summarise(_ context.Context, _ workitem.Item, from, to string) (verb.SummaryResult, error) {
	return verb.SummaryResult{Text: "summary of " + from + " → " + to}, nil
}
func (edgeSummariser) MandatorySummary(context.Context, workitem.Item) bool { return false }

type stubGater struct {
	dec verb.GateDecision
}

func (g stubGater) Gate(context.Context, workitem.Item, string) (verb.GateDecision, error) {
	return g.dec, nil
}

func dispatchRaw(t *testing.T, name string, req any) (json.RawMessage, error) {
	t.Helper()
	b, _ := json.Marshal(req)
	return verb.Dispatch(context.Background(), name, b)
}

func TestStorySetGatedRejectBlocksTransition(t *testing.T) {
	wire(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: false, Notes: "no acceptance criteria", Skill: "satelle-story-done-review"}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected reject to block the transition, got err=%v", err)
	}

	var after workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
	if after.Status != "in_progress" {
		t.Errorf("status changed to %q despite reject — gate did not block", after.Status)
	}
}

func TestStorySetGatedAcceptEnacts(t *testing.T) {
	wire(t)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "satelle-story-done-review"}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)

	var after workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &after)
	if after.Status != "done" {
		t.Errorf("accept should enact: status = %q, want done", after.Status)
	}
}

type createStub struct {
	dec verb.GateDecision
}

func (c createStub) ReviewCreate(context.Context, verb.CreateDraft) (verb.GateDecision, error) {
	return c.dec, nil
}

func TestStoryCreateGatedRejectBlocksPersist(t *testing.T) {
	wire(t)
	verb.SetCreateReviewer(createStub{dec: verb.GateDecision{Gated: true, Accept: false, Notes: "add numbered acceptance criteria", Skill: "satelle-story-review"}})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })

	_, err := dispatchRaw(t, "story-create", map[string]any{"title": "vague"})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected create to be rejected, got err=%v", err)
	}

	var items []workitem.Item
	json.Unmarshal(call(t, "story-list", map[string]any{}), &items)
	if len(items) != 0 {
		t.Errorf("rejected draft was persisted: %d items", len(items))
	}
}

func TestStoryCreateGatedAcceptPersists(t *testing.T) {
	wire(t)
	verb.SetCreateReviewer(createStub{dec: verb.GateDecision{Gated: true, Accept: true}})
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "well formed", "acceptance_criteria": "1. works"}), &it)
	if it.ID == "" {
		t.Error("accepted draft should persist with an id")
	}
}

func TestStorySetUngatedTransitionEnacts(t *testing.T) {
	wire(t)
	// No gater wired — the gateless baseline: transitions enact directly.
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "open"}), &it)

	var after workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &after)
	if after.Status != "done" {
		t.Errorf("ungated transition should enact: status = %q", after.Status)
	}
}

func TestGatedTransitionRecordsStepSummary(t *testing.T) {
	wire(t)
	dir := filepath.Join(t.TempDir(), ".satelle", "stories")
	verb.SetStoryDir(dir)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "satelle-story-done-review"}})
	sum := &summariserStub{out: "Moved to done after the criteria were met."}
	verb.SetStepSummariser(sum)
	t.Cleanup(func() { verb.SetTransitionGater(nil); verb.SetStepSummariser(nil); verb.SetStoryDir("") })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	if sum.calls != 1 {
		t.Fatalf("summariser ran %d times, want 1 (after the gated transition)", sum.calls)
	}
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindStepSummary}), &entries)
	if len(entries) != 1 || entries[0].Body != sum.out {
		t.Fatalf("expected one step_summary row with the prose, got %+v", entries)
	}
	// The summary is ALSO deposited as an attached step-summary doc, so a later
	// dispatched agent can PULL it via story docs/doc (the pull-context contract,
	// sty_47d31300).
	b, err := os.ReadFile(filepath.Join(dir, it.ID, "step-summary-in_progress-done.md"))
	if err != nil {
		t.Fatalf("step-summary doc not deposited: %v", err)
	}
	if !strings.Contains(string(b), sum.out) || !strings.Contains(string(b), "type: step-summary") {
		t.Errorf("step-summary doc missing prose or type frontmatter:\n%s", b)
	}
}

func TestPullContextChainResolvesPriorSummaries(t *testing.T) {
	// AC4/AC5 (sty_47d31300): the pull-context contract rests on the mandatory
	// step-summary chain. After two gated transitions, a dispatched agent can
	// reconstruct context via the SAME verbs the CLI pull commands dispatch to —
	// story-get (record), story-doc-list + story-doc-get (prior step summaries),
	// ledger-list (the step_summary rows).
	wire(t)
	dir := filepath.Join(t.TempDir(), ".satelle", "stories")
	verb.SetStoryDir(dir)
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "s"}})
	verb.SetStepSummariser(edgeSummariser{})
	t.Cleanup(func() { verb.SetTransitionGater(nil); verb.SetStepSummariser(nil); verb.SetStoryDir("") })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	// story-get resolves the record by id.
	var got workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &got)
	if got.ID != it.ID {
		t.Fatalf("story-get: want %s, got %s", it.ID, got.ID)
	}

	// story-doc-list lists BOTH step-summary docs.
	type docRef struct {
		Name, Type, Body string
	}
	var docs []docRef
	json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": it.ID}), &docs)
	want := map[string]bool{"step-summary-backlog-in_progress": false, "step-summary-in_progress-done": false}
	for _, d := range docs {
		if _, ok := want[d.Name]; ok {
			want[d.Name] = true
			if d.Type != "step-summary" {
				t.Errorf("doc %s type = %q, want step-summary", d.Name, d.Type)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("story-doc-list missing prior step summary %q", name)
		}
	}

	// story-doc-get returns each summary body.
	for edge, tofrom := range map[string]string{"step-summary-backlog-in_progress": "backlog → in_progress", "step-summary-in_progress-done": "in_progress → done"} {
		var d docRef
		json.Unmarshal(call(t, "story-doc-get", map[string]any{"story_id": it.ID, "name": edge}), &d)
		if !strings.Contains(d.Body, "summary of "+tofrom) {
			t.Errorf("story-doc-get %s body missing the edge summary:\n%s", edge, d.Body)
		}
	}

	// ledger-list filtered to step_summary returns both rows.
	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindStepSummary}), &entries)
	if len(entries) != 2 {
		t.Fatalf("want 2 step_summary ledger rows (one per gated transition), got %d", len(entries))
	}
}

func TestGatedTransitionRecordsAgentInvocation(t *testing.T) {
	wire(t)
	// An LLM reviewer carries its invocation: the resolved command and the injected
	// skill/rubric file. A functional check would leave Command empty (no agent).
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-story-done-review",
		Reviewers: []verb.ReviewerVerdict{{
			Skill:   "satelle-story-done-review",
			Accept:  true,
			Command: "claude -p --append-system-prompt {system}",
			Context: "satelle-story-done-review",
		}},
	}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindAgentInvocation}), &entries)
	if len(entries) != 1 {
		t.Fatalf("want one agent_invocation row, got %d: %+v", len(entries), entries)
	}
	e := entries[0]
	// AC: the entry names the agent and the injected skill/rubric file.
	if !strings.Contains(e.Body, "satelle-story-done-review") || !strings.Contains(e.Body, "claude") {
		t.Errorf("invocation body missing skill or command: %q", e.Body)
	}
	var p struct {
		Agent   string `json:"agent"`
		Command string `json:"command"`
		Context string `json:"context"`
	}
	json.Unmarshal(e.Payload, &p)
	if p.Agent != "reviewer" || p.Context != "satelle-story-done-review" || p.Command == "" {
		t.Errorf("invocation payload wrong: %+v", p)
	}
}

func TestFunctionalCheckRecordsNoInvocation(t *testing.T) {
	wire(t)
	// A deterministic functional-check gate invokes no agent (Command empty), so no
	// agent_invocation row is written — only LLM agent invocations are recorded.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-integration-check",
		Reviewers: []verb.ReviewerVerdict{{Skill: "satelle-integration-check", Accept: true}},
	}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	var entries []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindAgentInvocation}), &entries)
	if len(entries) != 0 {
		t.Errorf("functional check should record no agent_invocation, got %d", len(entries))
	}
}

func TestUngatedTransitionSkipsSummariser(t *testing.T) {
	wire(t)
	// No transition gater → ungated edge → summariser must not run.
	sum := &summariserStub{out: "should not run"}
	verb.SetStepSummariser(sum)
	t.Cleanup(func() { verb.SetStepSummariser(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "open"}), &it)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})

	if sum.calls != 0 {
		t.Errorf("summariser ran on an ungated transition (%d calls)", sum.calls)
	}
}

// TestStorySetSurfacesDecisionNotesReasoning pins AC8 (sty_e21cbc08): reject
// errors carry decision=, notes=, and reasoning=; accept enacts with the same
// fields on the review_accept ledger body (stderr is the live session notice).
func TestStorySetSurfacesDecisionNotesReasoning(t *testing.T) {
	wire(t)

	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: false, Skill: "satelle-story-done-review",
		Notes: "missing AC", Reasoning: "body lacks numbered criteria",
	}})
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "in_progress"}), &it)
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	if err == nil {
		t.Fatal("expected reject")
	}
	msg := err.Error()
	for _, want := range []string{"decision=reject", "notes=missing AC", "reasoning=body lacks numbered criteria"} {
		if !strings.Contains(msg, want) {
			t.Errorf("reject error missing %q: %s", want, msg)
		}
	}

	// Accept path: status enacts; acceptBody format is covered by the reject shape
	// and the pure formatReasoningSuffix internal test in workitem_internal_test.
	// Park the first story first so the single-story seat does not block engage.
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-story-done-review",
		Notes: "close", Reasoning: "park",
	}})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "done"})
	verb.SetTransitionGater(stubGater{dec: verb.GateDecision{
		Gated: true, Accept: true, Skill: "satelle-story-done-review",
		Notes: "all good", Reasoning: "ACs met in tree",
	}})
	var it2 workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "y", "status": "in_progress"}), &it2)
	var after workitem.Item
	json.Unmarshal(call(t, "story-set", map[string]any{"id": it2.ID, "status": "done"}), &after)
	if after.Status != "done" {
		t.Fatalf("accept should enact, status=%q", after.Status)
	}
	var accepts []ledger.Entry
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it2.ID, "kind": ledger.KindReviewAccept}), &accepts)
	if len(accepts) == 0 {
		t.Fatal("expected a review_accept ledger row")
	}
	body := accepts[0].Body
	for _, want := range []string{"decision=accept", "notes=all good", "reasoning=ACs met in tree"} {
		if !strings.Contains(body, want) {
			t.Errorf("accept ledger body missing %q: %s", want, body)
		}
	}
}
