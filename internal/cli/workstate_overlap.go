package cli

// Overlap is recorded, not arbitrated (sty_9914df10). When a sync pass finds a
// story's two copies disagreeing, the disagreement is recorded in this machine's
// work-state sync state, beside the reason, and reported from there — never
// appended to the story's chain (sty_199ad82b): an event the pass made would be
// local-only, so it would block the next push and fork the chain. The refusal and
// --force behave exactly as before. No story is held (sty_40853d02), so the other
// side is named from the latest working_on row another location wrote, when there
// is one.

import (
	"context"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// Where an overlap came from.
const overlapSourceConflict = "conflict"

// overlapUnknown names another side no working_on row tells us about.
const overlapUnknown = "unknown"

// recordStoryConflicts persists what a pass found about story disagreements, for
// the session-start advisory and `sync status` to read (sty_6e5fc41c). It writes
// even when the pass failed: a refused pull is exactly the conflict to record.
// Each conflict names this location and the other location that last worked the
// story when its working_on row is here ("unknown" otherwise).
func recordStoryConflicts(ctx context.Context, a *app.App, client *hosted.Client, project string, rep passReport) {
	local := "local"
	if client != nil && client.Location() != "" {
		local = client.Location()
	}
	var working map[string]workingOnRow
	if len(rep.Stories.Conflicts) > 0 {
		working = otherWorking(ctx, a)
	}
	conflicts := make(map[string]syncstate.StoryConflict, len(rep.Stories.Conflicts))
	for id, reason := range rep.Stories.Conflicts {
		remote := working[id].name()
		if remote == "" {
			remote = overlapUnknown
		}
		conflicts[id] = syncstate.StoryConflict{Reason: reason, LocalHolder: local, RemoteHolder: remote, Source: overlapSourceConflict}
	}
	_ = syncstate.RecordStoryConflictDetails(config.GlobalDir(), a.RepoRoot, conflicts, rep.Stories.Cleared, time.Now().UTC())
}
