package verb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestStoryCreateRecordsCreatorSessionModel pins AC4's creator capture
// (sty_7069bced): the creating session's own already-published in-loop model
// is re-recorded under the story's "creator" role — config.SelectModel's
// creator tier reads it back via verb.SessionModels.
func TestStoryCreateRecordsCreatorSessionModel(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "sess-creator")
	config.PublishSessionModel("sess-creator", verb.SessionModelRoleInLoop, "claude-opus-5-5", "claude", "")
	wire(t)

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	_, creator := verb.SessionModels(context.Background(), created.ID)
	if creator.Model != "claude-opus-5-5" || creator.Executable != "claude" {
		t.Fatalf("creator session model = %+v, want claude-opus-5-5/claude", creator)
	}
}

// TestStoryCreateRecordsCreatorSessionModelUnknown: no session published →
// "unknown" recorded, not silently skipped.
func TestStoryCreateRecordsCreatorSessionModelUnknown(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	wire(t)

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	_, creator := verb.SessionModels(context.Background(), created.ID)
	if creator.Model != "unknown" {
		t.Fatalf("creator session model = %+v, want unknown", creator)
	}
}

// TestStorySetEngagingRecordsInLoopSessionModel pins AC4's in-loop capture: a
// transition INTO an engaging status records the engaging session's model
// under the story's "in-loop" role, on every engaging transition (not just
// the first).
func TestStorySetEngagingRecordsInLoopSessionModel(t *testing.T) {
	wireWithWorkflows(t, singleStoryWF)
	t.Setenv(config.SessionEnv, "sess-engage-1")
	config.PublishSessionModel("sess-engage-1", verb.SessionModelRoleInLoop, "sonnet", "claude", "")

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if a.Status != "plan" {
		t.Fatalf("status = %q", a.Status)
	}
	inLoop, _ := verb.SessionModels(context.Background(), a.ID)
	if inLoop.Model != "sonnet" || inLoop.Executable != "claude" {
		t.Fatalf("in-loop session model = %+v, want sonnet/claude", inLoop)
	}
}

// TestStorySetEngagingRecordsInLoopSessionModelUnknown: no session published
// for this process (no SATELLE_SESSION, nothing PublishSessionModel'd) → the
// engaging transition still records a row, "unknown", rather than leaving the
// in-loop role silently unresolved.
func TestStorySetEngagingRecordsInLoopSessionModelUnknown(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	wireWithWorkflows(t, singleStoryWF)

	var a workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "First", "category": "feature"}), &a)
	json.Unmarshal(call(t, "story-set", map[string]any{"id": a.ID, "status": "plan"}), &a)
	if a.Status != "plan" {
		t.Fatalf("status = %q", a.Status)
	}
	inLoop, _ := verb.SessionModels(context.Background(), a.ID)
	if inLoop.Model != "unknown" {
		t.Fatalf("in-loop session model = %+v, want unknown", inLoop)
	}
}

// TestSessionModelsLatestPerRole: several rows per role land on the ledger
// across a story's life; the resolver returns the LAST (most recent) one.
func TestSessionModelsLatestPerRole(t *testing.T) {
	wire(t)
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	verb.RecordSessionModel(context.Background(), created.ID, "", verb.SessionModelRoleInLoop, "haiku", "claude", "sess-x", "")
	verb.RecordSessionModel(context.Background(), created.ID, "", verb.SessionModelRoleInLoop, "opus", "claude", "sess-x", "")

	inLoop, _ := verb.SessionModels(context.Background(), created.ID)
	if inLoop.Model != "opus" {
		t.Fatalf("in-loop session model = %+v, want the LAST recorded (opus)", inLoop)
	}
}

// TestSessionModelsIgnoresRetiredOrchestratorRow (sty_6f9ba7ca AC3): a
// session_model row under the retired "orchestrator" role — written by a
// story-chat session before the command was removed — is not read by any
// tier, so an in-loop model still resolves and nothing inherits the stale
// orchestrator's.
func TestSessionModelsIgnoresRetiredOrchestratorRow(t *testing.T) {
	wire(t)
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	verb.RecordSessionModel(context.Background(), created.ID, "", "orchestrator", "opus", "claude", "sess-x", "")
	verb.RecordSessionModel(context.Background(), created.ID, "", verb.SessionModelRoleInLoop, "haiku", "claude", "sess-x", "")

	inLoop, _ := verb.SessionModels(context.Background(), created.ID)
	if inLoop.Model != "haiku" {
		t.Fatalf("in-loop session model = %+v, want haiku (the orchestrator row must not be read)", inLoop)
	}
	model, source := config.SelectModel(config.SelectInput{
		CommandExecutable: "claude", HasModelSlot: true, InLoop: inLoop,
	})
	if model != "haiku" || source != config.ModelSourceInheritedInLoop {
		t.Fatalf("SelectModel = %q/%q, want haiku/inherited-in-loop", model, source)
	}
}

// TestRecordSessionModelCarriesSessionID pins AC5 (sty_719c4a7b): every
// session_model ledger row records the session id that reported it, so the
// column can be traced back to a driver rather than only a role/model pair.
func TestRecordSessionModelCarriesSessionID(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	verb.RecordSessionModel(ctx, created.ID, "", verb.SessionModelRoleInLoop, "grok-4.7", "grok", "01a0d2d8", "")

	entries, err := db.Ledger.ListByStory(ctx, created.ID, ledger.KindSessionModel)
	if err != nil {
		t.Fatalf("ListByStory: %v", err)
	}
	var row struct {
		SessionID string `json:"session_id"`
		Reason    string `json:"reason"`
	}
	found := false
	for _, e := range entries {
		if json.Unmarshal(e.Payload, &row) == nil && row.SessionID == "01a0d2d8" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no session_model row carried session_id=01a0d2d8, entries=%+v", entries)
	}
}

// TestSessionModelsParsesLegacyRowWithoutSessionID: a row written before
// sty_719c4a7b (no session_id/reason keys) still parses — the new fields are
// additive, not a breaking schema change.
func TestSessionModelsParsesLegacyRowWithoutSessionID(t *testing.T) {
	db := wire(t)
	ctx := context.Background()
	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	legacy, _ := json.Marshal(map[string]string{"role": verb.SessionModelRoleInLoop, "model": "unknown", "executable": "claude"})
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: created.ID, Kind: ledger.KindSessionModel, Payload: legacy,
	}, time.Now()); err != nil {
		t.Fatalf("append legacy row: %v", err)
	}
	inLoop, _ := verb.SessionModels(ctx, created.ID)
	if inLoop.Model != "unknown" || inLoop.Executable != "claude" {
		t.Fatalf("legacy row must still parse, got %+v", inLoop)
	}
}

// TestAppendAgentInvocationRoundTripsModelSource pins the write→read path a
// live session's close row depends on (sty_7069bced AC5): AppendAgentInvocation
// marshals model/model_source/model_resolved onto a REAL ledger.Store row, and
// ledger.EventTelemetry — the same reader `satelle story cost` and the web
// timeline use — decodes those three fields back off that stored row. Neither
// side was exercised together before: the engine-level tests stub the
// recorder, and EventTelemetry's own tests build an Entry in memory rather
// than reading one AppendAgentInvocation actually wrote.
func TestAppendAgentInvocationRoundTripsModelSource(t *testing.T) {
	db := wire(t)
	ctx := context.Background()

	var created workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "T", "body": "b", "acceptance": "1. x", "category": "feature",
	}), &created)

	if err := verb.AppendAgentInvocation(ctx, created.ID, map[string]any{
		"agent": "coder", "live": true, "phase": "close",
		"model": "y", "model_source": "agent", "model_resolved": "claude-sonnet-5",
	}); err != nil {
		t.Fatalf("AppendAgentInvocation: %v", err)
	}

	entries, err := db.Ledger.ListByStory(ctx, created.ID, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatalf("ListByStory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("agent_invocation rows = %d, want 1", len(entries))
	}

	tel := ledger.EventTelemetry(entries[0])
	if tel.Model != "y" || tel.ModelSource != "agent" || tel.ModelResolved != "claude-sonnet-5" {
		t.Fatalf("telemetry = %+v, want Model=y ModelSource=agent ModelResolved=claude-sonnet-5", tel)
	}
}
