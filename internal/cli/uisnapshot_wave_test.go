package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func waveReqJSON(t *testing.T, id string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sty_a42f4add AC1: the snapshot carries the story-wave verb's own response for
// every epic-parent — byte for byte, so the page cannot disagree with the CLI —
// and no wave row for a story that is not an epic-parent.
func TestBuildUISnapshotWaveRowIsTheVerbResponse(t *testing.T) {
	e := closeGuardEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(category string, tags ...string) workitem.Item {
		it, err := e.db.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: "w " + category, Body: "goal",
			AcceptanceCriteria: "1. x", Category: category, Status: workitem.StatusBacklog, Tags: tags,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	epic := mk("epic-parent", "epic:w")
	first := mk("fix", "epic:w")
	mk("fix", "epic:w", "depends-on:"+first.ID)
	plain := mk("chore")

	want, err := verb.Dispatch(ctx, "story-wave", waveReqJSON(t, epic.ID))
	if err != nil {
		t.Fatalf("story-wave: %v", err)
	}

	a := &app.App{Config: config.Config{}, RepoRoot: e.repo, Store: e.db}
	builds := map[string]func(context.Context, *app.App) (*mirror.Snapshot, error){
		"full":  buildUISnapshot,
		"drain": buildUIDrainSnapshot,
	}
	for name, build := range builds {
		snap, err := build(ctx, a)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(snap.Waves) != 1 {
			t.Fatalf("%s: want exactly one wave row (the epic-parent), got %d", name, len(snap.Waves))
		}
		if !bytes.Equal(snap.Waves[0], want) {
			t.Errorf("%s: pushed wave row differs from the verb response\n got: %s\nwant: %s", name, snap.Waves[0], want)
		}
		var row map[string]any
		if err := json.Unmarshal(snap.Waves[0], &row); err != nil || row["container"] != epic.ID {
			t.Errorf("%s: wave row container = %v, want %s", name, row["container"], epic.ID)
		}
		if bytes.Contains(snap.Waves[0], []byte(plain.ID)) {
			t.Errorf("%s: a non-epic story leaked into the wave row", name)
		}
	}
}

// A verb refusal is pushed as its error text under the container id, so the page
// shows the refusal the CLI prints.
func TestBuildUISnapshotWaveRowCarriesTheRefusal(t *testing.T) {
	e := closeGuardEnv(t)
	ctx := context.Background()
	chore, err := e.db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "not an epic", Body: "goal",
		AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusBacklog,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// The verb refuses a non-epic-parent; force the epic-parent branch by
	// presenting the same item as an epic-parent without storing the category.
	asEpic := chore
	asEpic.Category = "epic-parent"
	_, verbErr := verb.Dispatch(ctx, "story-wave", waveReqJSON(t, chore.ID))
	if verbErr == nil {
		t.Fatal("story-wave must refuse a story that is not an epic-parent")
	}
	waves := listWavesJSON(ctx, []workitem.Item{asEpic})
	if len(waves) != 1 {
		t.Fatalf("waves = %d, want 1", len(waves))
	}
	var row struct {
		Container string `json:"container"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(waves[0], &row); err != nil {
		t.Fatal(err)
	}
	if row.Container != chore.ID || row.Error != verbErr.Error() {
		t.Errorf("refusal row = %+v, want container %s error %q", row, chore.ID, verbErr.Error())
	}
}
