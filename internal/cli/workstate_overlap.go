package cli

// Overlap is recorded, not arbitrated (sty_9914df10). When a sync pass finds a
// story's two copies disagreeing, the pass writes one `overlap` row onto that
// story's ledger. The refusal and --force behave exactly as before; the row only
// makes the overlap part of the story, so it reaches the next push and every
// machine that pulls the story. No story is held (sty_40853d02), so the other side
// is named from the latest working_on row another location wrote, when there is
// one.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// Where an overlap came from.
const overlapSourceConflict = "conflict"

// overlapUnknown names another side no working_on row tells us about.
const overlapUnknown = "unknown"

// overlapPayload is the payload of an overlap row.
type overlapPayload struct {
	LocalHolder  string `json:"local_holder"`
	RemoteHolder string `json:"remote_holder"`
	Reason       string `json:"reason"`
	Source       string `json:"source"`
}

// recordOverlap appends the overlap row for a story unless the same overlap
// (story, source, remote location) is already recorded, and reports whether it
// appended one. remote is the latest working_on row another location wrote for the
// story, the zero row when there is none. Best-effort: a failure to write it never
// fails the sync pass.
func recordOverlap(ctx context.Context, a *app.App, client *hosted.Client, storyID, source string, remote workingOnRow, reason string) bool {
	if a == nil || a.Store == nil || storyID == "" {
		return false
	}
	local := "local"
	if client != nil && client.Location() != "" {
		local = client.Location()
	}
	remoteLoc, remoteName := remote.Location, remote.name()
	if remoteLoc == "" {
		remoteLoc = overlapUnknown
	}
	if remoteName == "" {
		remoteName = overlapUnknown
	}
	payload, _ := json.Marshal(overlapPayload{LocalHolder: local, RemoteHolder: remoteName, Reason: reason, Source: source})
	refs, _ := json.Marshal([]string{storyID, source, remoteLoc})
	_, created, err := a.Store.Ledger.AppendOnce(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindOverlap,
		Actor:   "sync",
		Body:    fmt.Sprintf("overlap (%s): %s — local %s, remote %s", source, reason, local, remoteName),
		Payload: payload,
		Refs:    refs,
	}, time.Now())
	return err == nil && created
}

// recordStoryConflicts persists what a pass found about story disagreements, for
// the session-start advisory to read (sty_6e5fc41c). It writes even when the
// pass failed: a refused pull is exactly the conflict to record. Each conflict
// not recorded before also becomes an overlap row on its story, naming the other
// location that last worked it when its working_on row is here ("unknown"
// otherwise).
func recordStoryConflicts(ctx context.Context, a *app.App, client *hosted.Client, project string, rep passReport) {
	global := config.GlobalDir()
	var known map[string]syncstate.StoryConflict
	if st, ok, err := syncstate.Load(global, a.RepoRoot); err == nil && ok {
		known = st.StoryConflicts
	}
	_ = syncstate.RecordStoryConflicts(global, a.RepoRoot, rep.Stories.Conflicts, rep.Stories.Cleared, time.Now().UTC())
	var working map[string]workingOnRow
	for _, id := range slices.Sorted(maps.Keys(rep.Stories.Conflicts)) {
		if _, seen := known[id]; seen {
			continue
		}
		if working == nil {
			working = otherWorking(ctx, a)
		}
		recordOverlap(ctx, a, client, id, overlapSourceConflict, working[id], rep.Stories.Conflicts[id])
	}
}
