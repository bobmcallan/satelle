package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
	wire(t)
	d := &dispatcherStub{err: errors.New("named agent architect failed performing step")}
	verb.SetExecutorDispatcher(d)
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })

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
	wire(t)
	d := &dispatcherStub{res: verb.DispatchResult{Dispatched: true, Agent: "architect", Command: "fake {system}"}}
	verb.SetExecutorDispatcher(d)
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })

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
