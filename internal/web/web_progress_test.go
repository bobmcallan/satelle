package web_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

const projectWFDoc = "---\nname: satelle-project-workflow\ntype: workflow\napplies_to: [\"*\"]\n---\ntransitions:\n" +
	"  - {from: backlog, to: in_progress}\n  - {from: in_progress, to: commit_push}\n" +
	"  - {from: commit_push, to: committed}\n  - {from: committed, to: done}\n"

const parentWFDoc = "---\nname: satelle-parent-workflow\ntype: workflow\napplies_to: [\"epic-parent\", \"parent\"]\n---\ntransitions:\n" +
	"  - {from: backlog, to: done}\n"

// planSpineWFDoc has plan as step 1 and in_progress as step 2 (the reviewer-only
// shape), so a story that jumped straight to in_progress skipped step 1.
const planSpineWFDoc = "---\nname: satelle-project-workflow\ntype: workflow\napplies_to: [\"*\"]\n---\ntransitions:\n" +
	"  - {from: backlog, to: plan}\n  - {from: plan, to: in_progress}\n" +
	"  - {from: in_progress, to: release}\n  - {from: release, to: done}\n  - {from: release, to: in_progress}\n"

// TestProgressLightsFillLeadingGap: an item whose first recorded transition lands
// mid-spine (it skipped an earlier step — e.g. engaged before the workflow gained
// the plan step) must still render its progress strip in order FROM 1, with the
// skipped leading step shown as a muted "not run" placeholder (sty_d9a0b573).
func TestProgressLightsFillLeadingGap(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()

	indexDocs(t, db, "workflows", map[string]string{"satelle-project-workflow": planSpineWFDoc})

	// A story that jumped backlog → in_progress (step 2), skipping plan (step 1).
	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Skipped plan", Category: "feature", Status: workitem.StatusInProgress}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, db, ledger.AppendInput{StoryID: it.ID, Kind: ledger.KindStatusTransition, Payload: transitionPayloadJSON("backlog", "in_progress")})

	_, body := get(t, srv.URL+"/")
	// The strip reads from 1: a muted placeholder for the skipped step 1 …
	if !strings.Contains(body, `review-light-pending" title="1. not run"`) {
		t.Errorf("expected a muted 'not run' placeholder at step 1 so the strip reads from 1")
	}
	// … then the PULSING current light at step 2 — the story is actively IN
	// in_progress, so its current step pulses (sty_1b170b73).
	if !strings.Contains(body, `review-light-current" title="current stage">2</span>`) {
		t.Errorf("expected the current (pulsing) light at step 2 for the in_progress story")
	}
	// The entry transition into the current state must NOT ALSO render as a
	// completed step-2 light (that would double the step number).
	if strings.Contains(body, `title="2. backlog → in_progress`) {
		t.Errorf("the entry into the current state must not render as a completed step-2 light")
	}
	// It must still start the strip at 1.
	if !strings.Contains(body, `>1</span>`) {
		t.Errorf("progress strip did not start from 1")
	}
}

func transitionPayloadJSON(from, to string) json.RawMessage {
	p, _ := json.Marshal(map[string]string{"from": from, "to": to})
	return p
}

func mustAppend(t *testing.T, db *store.DB, in ledger.AppendInput) {
	t.Helper()
	if _, err := db.Ledger.Append(context.Background(), in, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestProgressLightsPerCategoryWorkflow drives the wired page end-to-end: an
// epic-parent item is numbered against the PARENT workflow (done = step 1) and a
// wildcard (feature) item against the PROJECT workflow (done = step 4) — proving
// lights track each item's OWN active workflow, not a single hardcoded/longest
// resolver (sty_8dafac0e).
func TestProgressLightsPerCategoryWorkflow(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()

	indexDocs(t, db, "workflows", map[string]string{
		"satelle-project-workflow": projectWFDoc,
		"satelle-parent-workflow":  parentWFDoc,
	})

	epic, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Epic close", Category: "epic-parent", Status: "done"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	feat, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "Feature close", Category: "feature", Status: "done"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, db, ledger.AppendInput{StoryID: epic.ID, Kind: ledger.KindStatusTransition, Payload: transitionPayloadJSON("backlog", "done")})
	mustAppend(t, db, ledger.AppendInput{StoryID: feat.ID, Kind: ledger.KindStatusTransition, Payload: transitionPayloadJSON("committed", "done")})

	_, body := get(t, srv.URL+"/")
	if !strings.Contains(body, `title="1. backlog → done`) {
		t.Errorf("epic-parent done should be step 1 (parent workflow); not found in page")
	}
	if !strings.Contains(body, `title="4. committed → done`) {
		t.Errorf("feature done should be step 4 (project workflow); not found in page")
	}
	// Regression guard: the epic must NOT be numbered against the project spine.
	if strings.Contains(body, `title="4. backlog → done`) {
		t.Errorf("epic-parent was numbered against the wrong (project) workflow")
	}
}
