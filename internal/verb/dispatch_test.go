package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// dispatcherStub is a scripted verb.ExecutorDispatcher.
type dispatcherStub struct {
	res   verb.DispatchResult
	err   error
	calls int
}

func (d *dispatcherStub) DispatchExecutor(context.Context, workitem.Item, string) (verb.DispatchResult, error) {
	d.calls++
	return d.res, d.err
}

// A dispatch failure refuses the transition: the status stays unchanged and the
// error is surfaced (sty_fd427546).
func TestStorySetDispatchFailureRefusesTransition(t *testing.T) {
	withWiring(t)
	wire(t)
	d := &dispatcherStub{err: errors.New("named agent architect failed performing step")}
	verb.SetExecutorDispatcher(d)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("expected dispatch failure to refuse the transition, got err=%v", err)
	}
	if d.calls != 1 {
		t.Errorf("dispatcher calls = %d, want 1", d.calls)
	}
	var after workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
	if after.Status != "backlog" {
		t.Errorf("status changed to %q despite dispatch failure", after.Status)
	}
}

// A successful dispatch enacts the transition and the run does not advance
// status beyond the requested state.
func TestStorySetDispatchSuccessEnacts(t *testing.T) {
	withWiring(t)
	wire(t)
	d := &dispatcherStub{res: verb.DispatchResult{Dispatched: true, Agent: "architect", Command: "fake {system}"}}
	verb.SetExecutorDispatcher(d)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)

	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}); err != nil {
		t.Fatalf("dispatch success must enact: %v", err)
	}
	var after workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
	if after.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", after.Status)
	}
}

// A cloud-performed step (sty_82cffd60) is consumed by the transition commit
// exactly as a local perform result is: the status is enacted, and the
// agent_invocation row carries the session, branch and collected commit beside
// the unavailable-usage note.
func TestStorySetCloudDispatchEnactsAndRecordsSession(t *testing.T) {
	withWiring(t)
	db := wire(t)
	cloud := &verb.CloudDispatch{
		SessionID: "session_X", URL: "https://claude.ai/code/session_X",
		Branch: "claude/satelle-sty_x-ab12cd34", Commit: "0123456789abcdef", Doc: "ac-evidence",
	}
	d := &dispatcherStub{res: verb.DispatchResult{
		Dispatched: true, Agent: "coder", Command: cloud.URL, Cloud: cloud,
		UsageNote: verb.UsageNote{UsageUnavailableReason: "claude cloud: usage unavailable", CacheSplitUnavailable: true},
	}}
	verb.SetExecutorDispatcher(d)

	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}); err != nil {
		t.Fatalf("a cloud dispatch result must enact the transition: %v", err)
	}
	var after workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
	if after.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", after.Status)
	}
	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindAgentInvocation)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		var row struct {
			Agent          string              `json:"agent"`
			UsageAvailable bool                `json:"usage_available"`
			Reason         string              `json:"usage_unavailable_reason"`
			Cloud          *verb.CloudDispatch `json:"cloud"`
		}
		if json.Unmarshal(e.Payload, &row) != nil || row.Cloud == nil {
			continue
		}
		found = true
		if row.Agent != "coder" || row.UsageAvailable || row.Reason != "claude cloud: usage unavailable" || *row.Cloud != *cloud {
			t.Errorf("agent_invocation row = %+v (cloud %+v), want the cloud record and unavailable usage", row, row.Cloud)
		}
	}
	if !found {
		t.Fatalf("no agent_invocation row carried the cloud record: %+v", entries)
	}
}
