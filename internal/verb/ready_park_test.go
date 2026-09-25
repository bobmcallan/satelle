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

const readyParkLine = `park = { state = "blocked", gate = "satelle-story-blocked-review" }`

// readyParkWF declares a ready step and a park for the "*" lane and for
// epic-parent, but no park for the "noparker" lane (sty_b8a0d062).
var readyParkWF = routeHalves(
	`["*"]
obligations = ["raised", "ready", "coded", "closed"]
`+readyParkLine+`

[epic-parent]
obligations = ["raised", "ready", "coded", "closed"]
`+readyParkLine+`

[noparker]
obligations = ["raised", "ready", "coded", "closed"]
`,
	`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["ready"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

func newReadyStory(t *testing.T, category string) workitem.Item {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "category": category, "status": "backlog"}), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func statusOf(t *testing.T, id string) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-get", map[string]any{"id": id}), &it)
	return it
}

func TestReadyRejectParksBlockedWithNotes(t *testing.T) {
	for _, category := range []string{"feature", "epic-parent"} {
		t.Run(category, func(t *testing.T) {
			wireWithWorkflows(t, readyParkWF)
			verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, _ workitem.Item, to string) (verb.DispatchResult, error) {
				if to != "ready" {
					return verb.DispatchResult{}, nil
				}
				return verb.DispatchResult{Agent: "ready-reviewer"},
					&verb.PerformerReject{Notes: "premise wrong: body claims foo.go:40 is 64 bytes"}
			}))
			t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })
			it := newReadyStory(t, category)

			if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "ready"}); err != nil {
				t.Fatalf("a premise reject must park, not refuse: %v", err)
			}
			after := statusOf(t, it.ID)
			if after.Status != "blocked" {
				t.Fatalf("status = %q, want blocked", after.Status)
			}
			if after.ParkOrigin != "backlog" {
				t.Errorf("park origin = %q, want backlog", after.ParkOrigin)
			}
			var entries []ledger.Entry
			json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": it.ID}), &entries)
			found := false
			for _, e := range entries {
				if e.Kind == ledger.KindComment && strings.Contains(e.Body, "foo.go:40") {
					found = true
				}
			}
			if !found {
				t.Errorf("premise notes missing from the timeline: %+v", entries)
			}
		})
	}
}

func TestReadyAcceptEntersReady(t *testing.T) {
	wireWithWorkflows(t, readyParkWF)
	verb.SetExecutorDispatcher(&dispatcherStub{res: verb.DispatchResult{Dispatched: true, Agent: "ready-reviewer"}})
	t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })
	it := newReadyStory(t, "feature")
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, it.ID).Status; got != "ready" {
		t.Errorf("status = %q, want ready", got)
	}
}

func TestReadyOtherFailureDoesNotPark(t *testing.T) {
	cases := map[string]struct {
		category string
		err      error
	}{
		"crash":             {"feature", errors.New("agent timed out")},
		"empty notes":       {"feature", &verb.PerformerReject{}},
		"lane without park": {"noparker", &verb.PerformerReject{Notes: "premise wrong"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wireWithWorkflows(t, readyParkWF)
			verb.SetExecutorDispatcher(&dispatcherStub{err: c.err})
			t.Cleanup(func() { verb.SetExecutorDispatcher(nil) })
			it := newReadyStory(t, c.category)
			_, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "ready"})
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Fatalf("want a refusal, got %v", err)
			}
			if got := statusOf(t, it.ID).Status; got != "backlog" {
				t.Errorf("status = %q, want backlog (no park)", got)
			}
		})
	}
}
