package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_81aa4d8f: `story amend` is the gated correction of the definition fields
// `story set` freezes. These cases pin the gate (accept, reject, and the
// undeclared-gate refusal), the before/after record, where amend may run, and
// that the NEXT gate judges the amended text.

// amendWF is a two-lane route with a park and a terminal state: the "*" lane
// runs backlog → in_progress → done, and the docs lane runs backlog → draft →
// done, so a category amendment can be tested for re-laning and for stranding.
var amendWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[docs]
obligations = ["raised", "drafted", "published"]
park = { state = "blocked" }
`,
	`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[drafted]
status = "draft"
agent = "executor"
requires = ["raised"]

[published]
status = "done"
terminal = true
requires = ["drafted"]
`)

// amendStub is a scripted verb.AmendReviewer that records what it was asked to
// judge, so a case can assert the gate saw the before/after.
type amendStub struct {
	dec   verb.GateDecision
	err   error
	seen  verb.AmendDraft
	calls int
}

func (s *amendStub) ReviewAmend(_ context.Context, d verb.AmendDraft) (verb.GateDecision, error) {
	s.calls++
	s.seen = d
	return s.dec, s.err
}

func wireAmend(t *testing.T, stub *amendStub) *store.DB {
	t.Helper()
	db := wireWithWorkflowsStore(t, amendWF)
	verb.SetAmendReviewer(stub)
	t.Cleanup(func() { verb.SetAmendReviewer(nil) })
	return db
}

// engagedStory creates a story and moves it out of the entry state, so its
// definition fields are frozen.
func engagedStory(t *testing.T, title, accept string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": title, "body": "b", "acceptance_criteria": accept, "category": "feature",
	}), &it); err != nil {
		t.Fatal(err)
	}
	var engaged workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &engaged); err != nil {
		t.Fatal(err)
	}
	if engaged.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", engaged.Status)
	}
	return engaged
}

// AC1 — the gate can reject, and a rejection leaves the story untouched.
func TestAmendRejectedLeavesStoryUnchanged(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: false,
		Skill: "satelle-story-amend-review", Notes: "this weakens AC2 rather than correcting it"}}
	db := wireAmend(t, stub)
	before := engagedStory(t, "Frozen", "1. original")

	err := dispatchErr(t, "story-amend", map[string]any{
		"id": before.ID, "acceptance_criteria": "1. weaker", "reason": "make the gate pass",
	})
	for _, want := range []string{"rejected by", "satelle-story-amend-review", "weakens AC2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reject error %q should name %q", err, want)
		}
	}
	after, gerr := db.Stories.Get(context.Background(), before.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if after.AcceptanceCriteria != before.AcceptanceCriteria || after.Title != before.Title ||
		after.Body != before.Body || after.Category != before.Category ||
		!after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("a rejected amendment must change nothing: before=%+v after=%+v", before, after)
	}
	entries, lerr := db.Ledger.ListByStory(context.Background(), before.ID, ledger.KindDefinitionAmended)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(entries) != 0 {
		t.Errorf("rejected amendment wrote %d definition_amended rows", len(entries))
	}
	// The gate judged the proposal in context: the story, the state it sits in,
	// the reason, and the field's before/after.
	if stub.calls != 1 || stub.seen.Status != "in_progress" || stub.seen.Reason == "" {
		t.Fatalf("gate draft = %+v (calls %d)", stub.seen, stub.calls)
	}
	if len(stub.seen.Fields) != 1 || stub.seen.Fields[0].Field != "acceptance_criteria" ||
		stub.seen.Fields[0].Old != "1. original" || stub.seen.Fields[0].New != "1. weaker" {
		t.Fatalf("gate must see the before/after, got %+v", stub.seen.Fields)
	}
}

// AC2 — an accepted amendment updates the row and records old and new.
func TestAmendAcceptedRecordsBeforeAndAfter(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "satelle-story-amend-review", Notes: "AC2 was factually false"}}
	db := wireAmend(t, stub)
	before := engagedStory(t, "Wrong ACs", "1. original\n2. false claim")

	var amended workitem.Item
	if err := json.Unmarshal(call(t, "story-amend", map[string]any{
		"id": before.ID, "acceptance_criteria": "1. original\n2. corrected claim",
		"reason": "AC2 asserted a behaviour the system does not have",
	}), &amended); err != nil {
		t.Fatal(err)
	}
	if amended.AcceptanceCriteria != "1. original\n2. corrected claim" {
		t.Fatalf("row not amended: %q", amended.AcceptanceCriteria)
	}
	if amended.Title != before.Title || amended.Body != before.Body || amended.Status != before.Status {
		t.Errorf("amend touched more than the named field: %+v", amended)
	}
	entries, err := db.Ledger.ListByStory(context.Background(), before.ID, ledger.KindDefinitionAmended)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("definition_amended rows = %d, want 1", len(entries))
	}
	var payload struct {
		Reason string            `json:"reason"`
		Skill  string            `json:"skill"`
		Fields []verb.AmendField `json:"fields"`
		Extra  map[string]any    `json:"-"`
	}
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("payload: %v (%s)", err, entries[0].Payload)
	}
	if payload.Reason == "" || payload.Skill != "satelle-story-amend-review" {
		t.Errorf("payload reason/skill = %+v", payload)
	}
	if len(payload.Fields) != 1 {
		t.Fatalf("payload fields = %+v, want only the changed field", payload.Fields)
	}
	f := payload.Fields[0]
	if f.Field != "acceptance_criteria" || f.Old != "1. original\n2. false claim" || f.New != "1. original\n2. corrected claim" {
		t.Errorf("payload must carry the full old and new: %+v", f)
	}
	if !strings.Contains(entries[0].Body, "acceptance_criteria") {
		t.Errorf("ledger body should name the amended field: %q", entries[0].Body)
	}
}

// AC1 (fail closed) — no declared gate, no amendment. Both shapes of "nothing
// judges this": no reviewer wired at all, and a reviewer reporting that the
// workflow declares no amend_review hook.
func TestAmendRefusedWhenNoGateIsDeclared(t *testing.T) {
	db := wireWithWorkflowsStore(t, amendWF)
	verb.SetAmendReviewer(nil)
	before := engagedStory(t, "Ungated", "1. original")

	err := dispatchErr(t, "story-amend", map[string]any{
		"id": before.ID, "acceptance_criteria": "1. corrected", "reason": "wrong AC",
	})
	if !strings.Contains(err.Error(), "amend_review") {
		t.Errorf("refusal should name the hook to declare: %v", err)
	}

	stub := &amendStub{dec: verb.GateDecision{Gated: false}}
	verb.SetAmendReviewer(stub)
	t.Cleanup(func() { verb.SetAmendReviewer(nil) })
	err = dispatchErr(t, "story-amend", map[string]any{
		"id": before.ID, "acceptance_criteria": "1. corrected", "reason": "wrong AC",
	})
	if !strings.Contains(err.Error(), "amend_review") {
		t.Errorf("an ungated verdict must refuse, not pass: %v", err)
	}
	after, gerr := db.Stories.Get(context.Background(), before.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if after.AcceptanceCriteria != before.AcceptanceCriteria {
		t.Fatalf("ungated amendment landed: %q", after.AcceptanceCriteria)
	}
}

// AC1 input rules: a reason is mandatory, and an amendment that proposes no
// change is a no-op rather than a gate round-trip.
func TestAmendRequiresReasonAndIsIdempotent(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	wireAmend(t, stub)
	it := engagedStory(t, "Reasoned", "1. original")

	err := dispatchErr(t, "story-amend", map[string]any{"id": it.ID, "acceptance_criteria": "1. changed"})
	if !strings.Contains(err.Error(), "--reason") {
		t.Errorf("missing reason should say so: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("the gate ran on an invalid request (%d calls)", stub.calls)
	}
	// Proposing the value the story already holds changes nothing and judges nothing.
	var same workitem.Item
	if err := json.Unmarshal(call(t, "story-amend", map[string]any{
		"id": it.ID, "acceptance_criteria": "1. original", "reason": "no change",
	}), &same); err != nil {
		t.Fatal(err)
	}
	if same.AcceptanceCriteria != "1. original" || stub.calls != 0 {
		t.Errorf("no-op amend should not reach the gate: calls=%d", stub.calls)
	}
}

// AC3 — `story set` on a frozen field stays refused, and the refusal names the
// amend path.
func TestStorySetOnFrozenFieldStillRefusedAndNamesAmend(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	wireAmend(t, stub)
	it := engagedStory(t, "Frozen still", "1. original")

	err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. sneaky"})
	for _, want := range []string{"refusing to change frozen definition field(s)", "acceptance_criteria", "story amend " + it.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("freeze refusal %q should contain %q", err, want)
		}
	}
}

// AC4 — the next gate judges the AMENDED definition: a gater that rejects while
// the false AC is present accepts once it has been corrected.
func TestAmendedDefinitionIsWhatTheNextGateJudges(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	wireAmend(t, stub)
	it := engagedStory(t, "Judged", "1. original\n2. FALSE CLAIM")

	var judged workitem.Item
	verb.SetTransitionGater(gaterFunc(func(item workitem.Item, _ string) verb.GateDecision {
		judged = item
		if strings.Contains(item.AcceptanceCriteria, "FALSE CLAIM") {
			return verb.GateDecision{Gated: true, Accept: false, Skill: "ac-truth-review", Notes: "AC2 is not true of the system"}
		}
		return verb.GateDecision{Gated: true, Accept: true, Skill: "ac-truth-review"}
	}))
	t.Cleanup(func() { verb.SetTransitionGater(nil) })

	if err := dispatchErr(t, "story-set", map[string]any{"id": it.ID, "status": "done"}); !strings.Contains(err.Error(), "AC2 is not true") {
		t.Fatalf("expected the gate to reject the false AC, got %v", err)
	}
	if _, err := dispatchRaw(t, "story-amend", map[string]any{
		"id": it.ID, "acceptance_criteria": "1. original\n2. corrected claim", "reason": "AC2 was factually false",
	}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	var done workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "done"}), &done); err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" {
		t.Fatalf("status = %q, want the previously-rejecting edge to accept", done.Status)
	}
	if strings.Contains(judged.AcceptanceCriteria, "FALSE CLAIM") {
		t.Errorf("the gate judged a stale definition: %q", judged.AcceptanceCriteria)
	}
}

// AC5 — amend is reachable from the parked (blocked) state, and the story
// resumes in place: same id, amended definition, no cancel-and-re-raise.
func TestAmendFromBlockedThenResume(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	db := wireAmend(t, stub)
	it := engagedStory(t, "Parked over a wrong definition", "1. original\n2. wrong")

	var parked workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "blocked"}), &parked); err != nil {
		t.Fatal(err)
	}
	if parked.Status != "blocked" {
		t.Fatalf("park failed: %q", parked.Status)
	}
	var amended workitem.Item
	if err := json.Unmarshal(call(t, "story-amend", map[string]any{
		"id": it.ID, "acceptance_criteria": "1. original\n2. corrected", "reason": "parked because AC2 was wrong",
	}), &amended); err != nil {
		t.Fatalf("amend from blocked: %v", err)
	}
	if amended.Status != "blocked" {
		t.Errorf("amend moved status to %q — it must never transition", amended.Status)
	}
	var resumed workitem.Item
	if err := json.Unmarshal(call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}), &resumed); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.ID != it.ID || resumed.Status != "in_progress" ||
		resumed.AcceptanceCriteria != "1. original\n2. corrected" {
		t.Fatalf("expected the same story resumed with the amended definition: %+v", resumed)
	}
	// The trail reads park → amend → resume on one id.
	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range entries {
		switch e.Kind {
		case ledger.KindStatusTransition, ledger.KindDefinitionAmended:
			order = append(order, e.Kind+":"+e.Body)
		}
	}
	joined := strings.Join(order, " | ")
	for _, want := range []string{"in_progress → blocked", "definition_amended", "blocked → in_progress"} {
		if !strings.Contains(joined, want) {
			t.Errorf("trail %q missing %q", joined, want)
		}
	}
	amendAt, resumeAt := strings.Index(joined, "definition_amended"), strings.Index(joined, "blocked → in_progress")
	if amendAt < 0 || resumeAt < 0 || amendAt > resumeAt {
		t.Errorf("the amendment must be recorded before the resume: %s", joined)
	}
}

// AC5's boundary — a closed record is history: terminal and cancel states refuse
// the amendment and point at a superseding story.
func TestAmendRefusedOnTerminalAndCancelledStates(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	wireAmend(t, stub)

	closed := engagedStory(t, "Closed", "1. original")
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": closed.ID, "status": "done"}); err != nil {
		t.Fatal(err)
	}
	err := dispatchErr(t, "story-amend", map[string]any{
		"id": closed.ID, "acceptance_criteria": "1. corrected", "reason": "late correction",
	})
	for _, want := range []string{"terminal", "supersedes:" + closed.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("terminal refusal %q should name %q", err, want)
		}
	}

	cancelled := engagedStory(t, "Cancelled", "1. original")
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": cancelled.ID, "status": "cancelled"}); err != nil {
		t.Fatal(err)
	}
	err = dispatchErr(t, "story-amend", map[string]any{
		"id": cancelled.ID, "acceptance_criteria": "1. corrected", "reason": "late correction",
	})
	if !strings.Contains(err.Error(), "cancel sink") {
		t.Errorf("a cancel sink is not amendable: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("the gate ran on an unamendable state (%d calls)", stub.calls)
	}
}

// AC4's second half — amending the category re-lanes the route, so an amendment
// onto a lane that declares no such state is refused rather than stranding the
// story off-route.
func TestAmendCategoryOntoALaneWithoutTheStateIsRefused(t *testing.T) {
	stub := &amendStub{dec: verb.GateDecision{Gated: true, Accept: true, Skill: "amend-review"}}
	db := wireAmend(t, stub)
	it := engagedStory(t, "Re-laned", "1. original")

	err := dispatchErr(t, "story-amend", map[string]any{
		"id": it.ID, "category": "docs", "reason": "this is documentation work",
	})
	for _, want := range []string{"docs", "in_progress", "stranded off-route"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("off-route refusal %q should name %q", err, want)
		}
	}
	after, gerr := db.Stories.Get(context.Background(), it.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if after.Category != "feature" {
		t.Fatalf("refused category amendment landed: %q", after.Category)
	}
	// A category that keeps the story on a lane declaring its state is accepted.
	var relaned workitem.Item
	if err := json.Unmarshal(call(t, "story-amend", map[string]any{
		"id": it.ID, "category": "chore", "reason": "misfiled as a feature",
	}), &relaned); err != nil {
		t.Fatalf("in-lane category amendment: %v", err)
	}
	if relaned.Category != "chore" {
		t.Fatalf("category = %q, want chore", relaned.Category)
	}
}

// gaterFunc adapts a closure to verb.TransitionGater so a case can decide from
// the item under review.
type gaterFunc func(workitem.Item, string) verb.GateDecision

func (f gaterFunc) Gate(_ context.Context, item workitem.Item, to string) (verb.GateDecision, error) {
	return f(item, to), nil
}
