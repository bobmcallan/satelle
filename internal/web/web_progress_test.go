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
