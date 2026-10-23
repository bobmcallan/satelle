package storyfold

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// snapshotActor is the actor of a seeded snapshot: the binary, not a person.
const snapshotActor = "satelle"

// StatusSource says what decides the status of a story being seeded.
type StatusSource int

const (
	// StatusFromRow takes the status from the row seeded from. It is for the
	// story's own database: its legacy chain's transitions were chained in the
	// order sync happened to insert them, which is not always the order they
	// happened in, while the row is where the story really is. The snapshot carries
	// the status, so the fold equals the row whatever the transitions say.
	StatusFromRow StatusSource = iota
	// StatusFromChain leaves status to the chain's transitions when it has any.
	// It is for a row that arrived from the hosted copy beside a chain that was
	// pulled in event order: the hosted row is the older cache and the chain wins.
	StatusFromChain
)

// SeedItem gives a story whose chain predates the fold its starting point: one
// definition_snapshot event, taken from it (the story's row), linked onto the end
// of the chain, so the fold of the story equals the row — park origin included,
// and status as src says. A story whose chain already defines its title (created
// by this binary, or already seeded) gets nothing, so running it again appends
// nothing. It reports whether it appended.
//
// Only a story is seeded; a task's file is its source of truth and an execution
// is not folded.
func SeedItem(ctx context.Context, led *ledger.Store, it workitem.Item, src StatusSource, now time.Time) (bool, error) {
	if led == nil {
		return false, fmt.Errorf("storyfold: seed needs a ledger")
	}
	if it.Kind != workitem.KindStory {
		return false, nil
	}
	chain, err := led.StoryChain(ctx, it.ID)
	if err != nil {
		return false, err
	}
	if st, _ := Fold(chain, nil); st.Defined[FieldTitle] {
		return false, nil
	}
	withStatus := true
	if src == StatusFromChain {
		for _, e := range chain {
			if e.Kind == ledger.KindStatusTransition && TransitionTo(e) != "" {
				withStatus = false
				break
			}
		}
	}
	refs, _ := json.Marshal(map[string]string{ledger.KindDefinitionSnapshot: it.ID})
	_, added, err := led.AppendOnce(ctx, ledger.AppendInput{
		StoryID: it.ID, Kind: ledger.KindDefinitionSnapshot, Actor: snapshotActor,
		Body:    "definition snapshot seeded from the story row",
		Payload: SnapshotPayloadOf(it, withStatus), Refs: refs,
	}, now)
	if err != nil {
		return false, fmt.Errorf("storyfold: seed %s: %w", it.ID, err)
	}
	return added, nil
}

// unseededStories finds the stories whose chain defines no title: none of their
// events is a definition_snapshot or a story_created that carries one. One
// indexed query, so a database with nothing to seed pays no write lock.
const unseededStories = `
SELECT w.id FROM work_items w
WHERE w.kind = 'story' AND NOT EXISTS (
    SELECT 1 FROM evidence e
    WHERE e.story_id = w.id AND (
        e.kind = ? OR (e.kind = ? AND CASE WHEN json_valid(e.payload)
            THEN json_extract(e.payload, '$.title') END IS NOT NULL)))
ORDER BY w.id`

// Seed seeds every story whose chain predates the fold, from its row, and
// returns how many it seeded. It is called once, when the database is opened,
// after every migration has settled the rows. led is the wired ledger, so each
// snapshot carries the workflow version now governing its story and the chain
// gains the workflow_version event that archives it — which is also how a story
// whose events recorded hashes before versions were archived gets one back.
func Seed(ctx context.Context, db *sql.DB, led *ledger.Store, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx, unseededStories, ledger.KindDefinitionSnapshot, ledger.KindStoryCreated)
	if err != nil {
		return 0, fmt.Errorf("storyfold: seed: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("storyfold: seed: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close() // the pool holds one connection; the writes below need it
	if err != nil {
		return 0, fmt.Errorf("storyfold: seed: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	stories := workitem.New(db)
	seeded := 0
	for _, id := range ids {
		it, err := stories.Get(ctx, id)
		if err != nil {
			return seeded, fmt.Errorf("storyfold: seed %s: %w", id, err)
		}
		added, err := SeedItem(ctx, led, it, StatusFromRow, now)
		if err != nil {
			return seeded, err
		}
		if added {
			seeded++
		}
	}
	return seeded, nil
}
