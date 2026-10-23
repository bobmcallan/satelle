package cli

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/verb"
)

// failingCreateReviewer fails the test when the create gate is consulted.
type failingCreateReviewer struct{ t *testing.T }

func (r failingCreateReviewer) ReviewCreate(context.Context, verb.CreateDraft) (verb.GateDecision, error) {
	r.t.Error("the structure-fix story re-entered the create gate")
	return verb.GateDecision{}, nil
}

// sty_d84be8bc AC1: the story reindex files for a doc that fails its structure
// check starts its chain with the same full story_created every story gets — its
// title, body, criteria, category, tags and status — yet is still created directly
// through the store: the create reviewer is not dispatched and no workflow stamp
// is added to its tags.
func TestReindexFiledStoryStartsItsChainWithoutTheCreateGate(t *testing.T) {
	repo := tempRepo(t)
	if _, err := runRoot(t, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	withVerbWiring(t)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app in %s: %v", repo, err)
	}
	t.Cleanup(func() { _ = a.Close() })
	verb.SetCreateReviewer(failingCreateReviewer{t})

	ctx := context.Background()
	id, filed, err := fileSystemStory(ctx, a, docindex.DocRef{Kind: "workflows", Name: "done"}, `done.toml: unknown key(s): "*".park.agent`)
	if err != nil || !filed {
		t.Fatalf("fileSystemStory = (%q, %v, %v)", id, filed, err)
	}
	it, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range it.Tags {
		if len(tag) >= len("workflow:") && tag[:len("workflow:")] == "workflow:" {
			t.Errorf("a workflow stamp was added to the filed story: %v", it.Tags)
		}
	}

	rows, err := a.Store.Ledger.ListByStory(ctx, id, ledger.KindStoryCreated)
	if err != nil || len(rows) != 1 {
		t.Fatalf("story_created rows = %d, %v; want 1", len(rows), err)
	}
	var p storyfold.CreatedPayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != it.Title || p.Body != it.Body || p.AcceptanceCriteria != it.AcceptanceCriteria ||
		p.Category != "system" || p.Status != it.Status || !slices.Equal(p.Tags, it.Tags) || p.Source != "reindex" {
		t.Errorf("story_created payload = %+v, want the filed story's definition %+v from reindex", p, it)
	}
	if drifts, err := verb.StoryDrifts(ctx, a.Store.Ledger, it); err != nil || len(drifts) != 0 {
		t.Errorf("the filed story's row differs from its chain: %+v %v", drifts, err)
	}
}
