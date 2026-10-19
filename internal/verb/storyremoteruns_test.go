package verb_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestRouteListsRemoteRuns: a story no remote has run reads exactly as before; a
// remote_run row adds a "Remote runs" line naming the step, remote, version,
// host (or that it is unnamed), identity label, times and run id.
func TestRouteListsRemoteRuns(t *testing.T) {
	wireRoute(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "run me", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &it)

	before, err := verb.StoryRoute(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "Remote runs") {
		t.Errorf("a story with no remote run must not carry the section:\n%s", before)
	}

	for _, p := range []ledger.RemoteRunPayload{
		{Remote: "edge", Version: "1.2.3", Host: "box-7", Label: "lbl", Step: "plan", RunID: "run_aaaaaaaa", Started: "2026-10-20T09:00:00Z", Ended: "2026-10-20T09:01:00Z"},
		{Remote: "edge", Version: "1.2.3", Label: "lbl", Step: "in_progress", RunID: "run_bbbbbbbb", Started: "2026-10-20T09:02:00Z", Ended: "2026-10-20T09:03:00Z", Error: "host.run: exit status 3"},
	} {
		raw, _ := json.Marshal(p)
		call(t, "ledger-append", map[string]any{"story_id": it.ID, "kind": ledger.KindRemoteRun, "payload": json.RawMessage(raw)})
	}
	got, err := verb.StoryRoute(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Remote runs",
		"- plan on remote edge @ satelle 1.2.3 · host box-7 · as lbl · 2026-10-20T09:00:00Z → 2026-10-20T09:01:00Z · run run_aaaaaaaa\n",
		"- in_progress on remote edge @ satelle 1.2.3 · host (unnamed) · as lbl · 2026-10-20T09:02:00Z → 2026-10-20T09:03:00Z · run run_bbbbbbbb · failed: host.run: exit status 3\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("route lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "run_aaaaaaaa") > strings.Index(got, "run_bbbbbbbb") {
		t.Errorf("runs are not in time order:\n%s", got)
	}
}
