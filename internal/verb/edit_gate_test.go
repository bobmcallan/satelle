package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_a8885220: an edit to a story's definition while it is still in its
// workflow's entry state is judged by the create gate, and the story_updated
// row names the editor and carries the before and after of each changed field.

// editGateReviewer answers ReviewCreate from a script and keeps every draft.
type editGateReviewer struct {
	accept bool
	drafts []verb.CreateDraft
}

func (r *editGateReviewer) ReviewCreate(_ context.Context, d verb.CreateDraft) (verb.GateDecision, error) {
	r.drafts = append(r.drafts, d)
	if r.accept {
		return verb.GateDecision{Gated: true, Accept: true, Skill: "create-rubric", Command: "stub-reviewer"}, nil
	}
	return verb.GateDecision{Gated: true, Skill: "create-rubric", Notes: "AC 1 is not verifiable", Command: "stub-reviewer"}, nil
}

// storyUpdated decodes a story_updated row's payload.
type storyUpdated struct {
	SessionID           string `json:"session_id"`
	Executable          string `json:"executable"`
	IdentityUnavailable string `json:"identity_unavailable"`
	Fields              []struct{ Field, Old, New string }
}

func storyUpdatedRows(t *testing.T, id string) []storyUpdated {
	t.Helper()
	var out []storyUpdated
	for _, e := range ledgerRows(t, id, ledger.KindStoryUpdated) {
		var p storyUpdated
		if len(e.Payload) > 0 {
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatalf("story_updated payload %q: %v", e.Payload, err)
			}
		}
		out = append(out, p)
	}
	return out
}

// newEditGateRig wires a create reviewer that accepts until a test flips it, so
// the story's own creation is judged and accepted before the edit under test.
func newEditGateRig(t *testing.T) *editGateReviewer {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	rev := &editGateReviewer{accept: true}
	verb.SetCreateReviewer(rev)
	return rev
}

func TestEditCreateGateAcceptedEditRecordsEditorAndBeforeAfter(t *testing.T) {
	rev := newEditGateRig(t)
	it := newFeature(t)
	rev.drafts = nil // the create itself was judged too
	accepted := len(ledgerRows(t, it.ID, ledger.KindReviewAccept))

	t.Setenv(config.SessionEnv, "sess-edit")
	// A non-Claude adapter's session: the executable must be named, not defaulted.
	config.PublishSessionModel("sess-edit", verb.SessionModelRoleInLoop, "grok-4", agentcli.HarnessGrok, "")

	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. narrowed"})

	if len(rev.drafts) != 1 || rev.drafts[0].AcceptanceCriteria != "1. narrowed" || rev.drafts[0].Title != "T" {
		t.Fatalf("the gate must judge the story as it would stand after the edit: %+v", rev.drafts)
	}
	if got := statusOf(t, it.ID).AcceptanceCriteria; got != "1. narrowed" {
		t.Errorf("accepted edit not stored: %q", got)
	}
	if got := len(ledgerRows(t, it.ID, ledger.KindReviewAccept)); got != accepted+1 {
		t.Errorf("review_accept rows = %d, want the edit's run recorded (%d before)", got, accepted)
	}
	rows := storyUpdatedRows(t, it.ID)
	if len(rows) != 1 {
		t.Fatalf("story_updated rows = %d, want 1", len(rows))
	}
	p := rows[0]
	if p.SessionID != "sess-edit" || p.Executable != agentcli.HarnessGrok || p.IdentityUnavailable != "" {
		t.Errorf("editor = %+v, want session sess-edit on grok", p)
	}
	if len(p.Fields) != 1 || p.Fields[0].Field != "acceptance_criteria" || p.Fields[0].Old != "1. one" || p.Fields[0].New != "1. narrowed" {
		t.Errorf("fields = %+v, want the acceptance_criteria before and after", p.Fields)
	}
	// The sibling definition_edited row names the same editor.
	var de verb.DefinitionEdit
	edits := ledgerRows(t, it.ID, ledger.KindDefinitionEdited)
	if len(edits) != 1 || json.Unmarshal(edits[0].Payload, &de) != nil || de.SessionID != "sess-edit" || de.Executable != agentcli.HarnessGrok {
		t.Errorf("definition_edited row = %s, want the same editor", edits[0].Payload)
	}
}

func TestEditCreateGateRejectedEditKeepsThePreviousText(t *testing.T) {
	rev := newEditGateRig(t)
	it := newFeature(t)
	rev.accept, rev.drafts = false, nil

	err := dispatchErr(t, "story-set", map[string]any{
		"id": it.ID, "title": "T2", "body": "B2", "acceptance_criteria": "1. vague", "category": "feature",
	})
	if !strings.Contains(err.Error(), "rejected by create-rubric") || !strings.Contains(err.Error(), "AC 1 is not verifiable") {
		t.Errorf("want the reviewer's objection, got %v", err)
	}
	got := statusOf(t, it.ID)
	if got.Title != "T" || got.Body != "B" || got.AcceptanceCriteria != "1. one" {
		t.Errorf("a rejected edit must leave the stored definition alone: %+v", got)
	}
	for _, kind := range []string{ledger.KindStoryUpdated, ledger.KindDefinitionEdited} {
		if n := len(ledgerRows(t, it.ID, kind)); n != 0 {
			t.Errorf("%s rows = %d after a rejected edit, want 0", kind, n)
		}
	}
	if n := len(ledgerRows(t, it.ID, ledger.KindReviewReject)); n != 1 {
		t.Errorf("review_reject rows = %d, want the rejected run recorded once", n)
	}
}

func TestEditCreateGateNotRunAfterTheEntryState(t *testing.T) {
	rev := newEditGateRig(t)
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	rev.accept, rev.drafts = false, nil // would reject, if asked

	call(t, "story-set", map[string]any{"id": it.ID, "acceptance_criteria": "1. applied at plan"})
	if len(rev.drafts) != 0 {
		t.Errorf("the create gate judged an edit outside the entry state: %+v", rev.drafts)
	}
	if got := statusOf(t, it.ID).AcceptanceCriteria; got != "1. applied at plan" {
		t.Errorf("edit at plan not stored: %q", got)
	}
}

func TestEditCreateGateOnlyJudgesDefinitionFields(t *testing.T) {
	rev := newEditGateRig(t)
	it := newFeature(t)
	rev.accept, rev.drafts = false, nil

	call(t, "story-set", map[string]any{"id": it.ID, "priority": "high", "tags": []string{"x"}})
	if len(rev.drafts) != 0 {
		t.Errorf("a priority/tag edit ran the create gate: %+v", rev.drafts)
	}
	rows := storyUpdatedRows(t, it.ID)
	if len(rows) != 1 || len(rows[0].Fields) != 0 {
		t.Errorf("a non-definition edit still names its editor, with no fields: %+v", rows)
	}
}

// A row whose session cannot be resolved says so, naming what it looked for,
// rather than carrying an empty session and executable.
func TestStoryUpdatedRowStatesAnUnresolvableSessionAsUnavailable(t *testing.T) {
	newEditGateRig(t)
	t.Setenv(config.SessionEnv, "")
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := agentcli.InLoopHarnessFromEnv([]string{kv}); ok || len(agentcli.SessionIDFromEnv([]string{kv})) > 0 {
			// Prefix markers match any value, so blanking is not enough: unset
			// (t.Setenv first, so the original is restored afterwards).
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	it := newFeature(t)

	call(t, "story-set", map[string]any{"id": it.ID, "title": "T2"})

	rows := storyUpdatedRows(t, it.ID)
	if len(rows) != 1 {
		t.Fatalf("story_updated rows = %d, want 1", len(rows))
	}
	p := rows[0]
	if p.SessionID != "" || p.Executable != "" {
		t.Fatalf("test environment still resolves an editor: %+v", p)
	}
	if !strings.Contains(p.IdentityUnavailable, "session:") || !strings.Contains(p.IdentityUnavailable, "executable:") ||
		!strings.Contains(p.IdentityUnavailable, agentcli.HarnessGrok) {
		t.Errorf("identity_unavailable = %q, want both gaps named with the adapters tried", p.IdentityUnavailable)
	}
	if len(p.Fields) != 1 || p.Fields[0].Field != "title" || p.Fields[0].Old != "T" || p.Fields[0].New != "T2" {
		t.Errorf("fields = %+v, want the title before and after", p.Fields)
	}
}

// Rows written before the editor was recorded carry no payload (story_updated)
// or a payload without the identity keys (definition_edited); both still parse.
func TestLegacyEditRowsStillParse(t *testing.T) {
	legacy := []ledger.Entry{
		{Kind: ledger.KindStoryUpdated},
		{Kind: ledger.KindStoryUpdated, Payload: json.RawMessage(`{}`)},
		{Kind: ledger.KindDefinitionEdited, Actor: "bob", Payload: json.RawMessage(`{"field":"title","old":"a","new":"b","actor":"bob"}`)},
	}
	edits := verb.DefinitionEditsFrom(legacy)
	if len(edits) != 1 || edits[0].Field != "title" || edits[0].Old != "a" || edits[0].New != "b" ||
		edits[0].SessionID != "" || edits[0].IdentityUnavailable != "" {
		t.Errorf("legacy definition_edited row = %+v, want it parsed with no editor", edits)
	}
	var p storyUpdated
	for _, e := range legacy[:2] {
		if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) != nil {
			t.Errorf("legacy story_updated payload %q does not parse", e.Payload)
		}
	}
}
