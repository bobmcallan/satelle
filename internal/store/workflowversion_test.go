package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func versionEvents(t *testing.T, db *DB, id string) []ledger.WorkflowSource {
	t.Helper()
	rows, err := db.Ledger.ListByStory(context.Background(), id, ledger.KindWorkflowVersion)
	if err != nil {
		t.Fatal(err)
	}
	var out []ledger.WorkflowSource
	for _, e := range rows {
		var src ledger.WorkflowSource
		if err := json.Unmarshal(e.Payload, &src); err != nil {
			t.Fatal(err)
		}
		out = append(out, src)
	}
	return out
}

// sty_d84be8bc AC7: each distinct workflow hash a story's events record has a
// workflow_version event in the chain — once per hash — carrying the hash and the
// route source that produced it, which re-hashes to the recorded hash; and a
// chain pulled to another machine carries it.
func TestEachRecordedWorkflowHashHasOneVersionEventThatRehashes(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	dir := seedRoute(t, db, hashRouteDone, hashRouteStep)
	story, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "versioned"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	first := wantVersion(t, db, story)

	// Three events under one route version: one version event.
	var events []ledger.Entry
	for range 3 {
		e, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: "noise"}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if got := versionEvents(t, db, story.ID); len(got) != 1 || got[0].Hash != first {
		t.Fatalf("after three events under one version: %+v, want exactly the one for %s", got, first)
	}

	// The route changes: the next event records the new hash and gets its own version.
	if err := os.WriteFile(filepath.Join(dir, "step.toml"), []byte(hashRouteStep+"\n# edited\n[extra]\nstatus = \"backlog\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": dir}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: story.ID, Kind: "noise"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if next.WorkflowHash == first || next.WorkflowHash != wantVersion(t, db, story) {
		t.Fatalf("the edited route did not change the recorded hash: %q", next.WorkflowHash)
	}
	got := versionEvents(t, db, story.ID)
	if len(got) != 2 {
		t.Fatalf("version events = %+v, want exactly two (one per hash)", got)
	}
	// Each carries the source that hashes to it, and every recorded hash is covered.
	have := map[string]bool{}
	for _, src := range got {
		if src.Done == "" || src.Step == "" || wfgovern.RouteHash(src.Done, src.Step) != src.Hash {
			t.Errorf("version %s: the carried source does not re-hash to it: %+v", src.Hash, src)
		}
		have[src.Hash] = true
	}
	chain, err := db.Ledger.StoryChain(ctx, story.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range chain {
		if e.Kind != ledger.KindWorkflowVersion && e.WorkflowHash != "" && !have[e.WorkflowHash] {
			t.Errorf("event %s records hash %s with no workflow_version in the chain", e.ID, e.WorkflowHash)
		}
	}
	// The version precedes the first event that records it, and the chain holds.
	if vi, ei := indexOfKind(chain, ledger.KindWorkflowVersion), indexOfID(chain, events[0].ID); vi < 0 || vi > ei {
		t.Errorf("the first version event is at %d, after the event %d that records it", vi, ei)
	}
	if rep, err := db.Ledger.VerifyAll(ctx); err != nil || len(rep.Breaks) != 0 {
		t.Errorf("chain = %+v, %v", rep, err)
	}

	// A pull: another machine receives the chain's rows as the sync carries them.
	other := openTemp(t)
	for _, e := range chain {
		if _, err := other.Ledger.Upsert(ctx, e, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	pulled := versionEvents(t, other, story.ID)
	if len(pulled) != 2 {
		t.Fatalf("the pulled chain carries %d version events, want 2", len(pulled))
	}
	for _, src := range pulled {
		if wfgovern.RouteHash(src.Done, src.Step) != src.Hash || !have[src.Hash] {
			t.Errorf("pulled version %s does not re-hash", src.Hash)
		}
	}
}

// The pull of a chain the other machine made under a workflow this one does not
// have replays against the archived source, not against this machine's workflow.
func TestAPulledChainReplaysAgainstTheSourceItCarries(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	seedRoute(t, db, hashRouteDone, hashRouteStep)
	story, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "replayed", Category: "feature"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: story.ID, Kind: ledger.KindStatusTransition,
		Payload: json.RawMessage(`{"from":"backlog","to":"in_progress"}`),
	}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	chain, _ := db.Ledger.StoryChain(ctx, story.ID)
	rep := storyfold.Replay(chain)
	if len(rep.Breaks) != 0 || rep.Checked != 1 {
		t.Errorf("replay = %+v, want the transition checked against its archived version", rep)
	}
}

func indexOfKind(chain []ledger.Entry, kind string) int {
	for i, e := range chain {
		if e.Kind == kind {
			return i
		}
	}
	return -1
}

func indexOfID(chain []ledger.Entry, id string) int {
	for i, e := range chain {
		if e.ID == id {
			return i
		}
	}
	return -1
}
