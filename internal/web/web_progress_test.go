package web_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
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

// TestProgressLightsStartingWhileSeatHeld: a backlog story with a live
// pre-transition engagement lease (in_flight, no ledger transitions) renders a
// single pulsing step-0 "starting" light; a sibling backlog with no lease stays
// blank; after the first transition lands the 0 light is gone (sty_e1314fe3).
func TestProgressLightsStartingWhileSeatHeld(t *testing.T) {
	srv, db := newServer(t)
	ctx := context.Background()

	indexDocs(t, db, "workflows", map[string]string{"satelle-project-workflow": projectWFDoc})

	held, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Seat held pre-transition", Category: "feature", Status: workitem.StatusBacklog,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	idle, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "Never engaged", Category: "feature", Status: workitem.StatusBacklog,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Plant a live in-flight seat (the acquire-before-gate window) without any
	// status_transition ledger row — status stays backlog, entered==false.
	if _, out, _, aerr := db.Leases.Acquire(ctx, held.ID, "story", "test-owner", "in_progress", true); aerr != nil || out != lease.OutcomeAcquired {
		t.Fatalf("acquire seat: out=%v err=%v", out, aerr)
	}

	_, body := get(t, srv.URL+"/")
	// AC1: seat-held backlog shows the pulsing starting light numbered 0.
	if !strings.Contains(body, `review-light-current" title="starting">0</span>`) {
		t.Errorf("expected step-0 starting light for seat-held story; body snippet missing")
	}
	// AC4: idle backlog has no progress lights at all (blank strip — no phantom 0).
	// Scope the idle row: its title must not be followed by a review-light span
	// before the next row ends — assert the idle story's row has an empty col-reviews.
	idleMarker := `data-title="never engaged"`
	idx := strings.Index(strings.ToLower(body), idleMarker)
	if idx < 0 {
		t.Fatalf("idle story row not found")
	}
	// Find the col-reviews cell within this row.
	rowSlice := body[idx:]
	if end := strings.Index(rowSlice, "</tr>"); end >= 0 {
		rowSlice = rowSlice[:end]
	}
	if strings.Contains(rowSlice, "review-light") {
		t.Errorf("idle backlog row must have a blank progress strip, got lights in: %s", rowSlice)
	}
	_ = idle

	// AC3: once a status_transition lands, the 0 light is gone and step-1 current renders.
	// Confirm settles in_flight (post-commit) and we record the entry transition.
	if err := db.Leases.Confirm(ctx, held.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	st := workitem.StatusInProgress
	if _, err := db.Stories.Update(ctx, held.ID, workitem.UpdateInput{Status: &st}, time.Now()); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, db, ledger.AppendInput{
		StoryID: held.ID, Kind: ledger.KindStatusTransition,
		Payload: transitionPayloadJSON("backlog", "in_progress"),
	})

	_, body = get(t, srv.URL+"/")
	if strings.Contains(body, `title="starting">0</span>`) {
		t.Errorf("starting light must disappear once the first transition lands")
	}
	if !strings.Contains(body, `review-light-current" title="current stage">1</span>`) {
		t.Errorf("expected step-1 current light after first transition")
	}
}
