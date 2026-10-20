package cli

// Overlap is recorded, not arbitrated (sty_9914df10). When a sync pass finds a
// story's two copies disagreeing, or a push is refused because another location
// or user holds the story, the pass writes one `overlap` row onto that story's
// ledger. The refusal and --force behave exactly as before; the row only makes
// the overlap part of the story, so it reaches the next push and every machine
// that pulls the story.

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
const (
	overlapSourceConflict = "conflict"
	overlapSourceHold     = "hold"
)

// overlapUnknown names a holder the server could not tell us about.
const overlapUnknown = "unknown"

// overlapPayload is the payload of an overlap row.
type overlapPayload struct {
	LocalHolder  string `json:"local_holder"`
	RemoteHolder string `json:"remote_holder"`
	RemoteUser   string `json:"remote_user,omitempty"`
	Reason       string `json:"reason"`
	Source       string `json:"source"`
}

// overlapHolderName is how a hold is named in an overlap row: its label when it
// has one, else its location, else "unknown".
func overlapHolderName(h hosted.HoldState) string {
	switch {
	case h.Label != "":
		return h.Label
	case h.LocationID != "":
		return h.LocationID
	}
	return overlapUnknown
}

// recordOverlap appends the overlap row for a story unless the same overlap
// (story, source, remote location) is already recorded, and reports whether it
// appended one. Best-effort: a failure to write it never fails the sync pass.
func recordOverlap(ctx context.Context, a *app.App, client *hosted.Client, storyID, source string, remote hosted.HoldState, reason string) bool {
	if a == nil || a.Store == nil || storyID == "" {
		return false
	}
	local := "local"
	if client != nil && client.Location() != "" {
		local = client.Location()
	}
	remoteLoc := remote.LocationID
	if remoteLoc == "" {
		remoteLoc = overlapUnknown
	}
	payload, _ := json.Marshal(overlapPayload{
		LocalHolder: local, RemoteHolder: overlapHolderName(remote), RemoteUser: remote.UserID, Reason: reason, Source: source,
	})
	refs, _ := json.Marshal([]string{storyID, source, remoteLoc})
	_, created, err := a.Store.Ledger.AppendOnce(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindOverlap,
		Actor:   "sync",
		Body:    fmt.Sprintf("overlap (%s): %s — local %s, remote %s", source, reason, local, overlapHolderName(remote)),
		Payload: payload,
		Refs:    refs,
	}, time.Now())
	return err == nil && created
}

// recordHeldOverlaps writes the overlap row for each push refusal and returns
// how many new rows it wrote.
func recordHeldOverlaps(ctx context.Context, a *app.App, client *hosted.Client, held ...*hosted.HeldError) int {
	var n int
	for _, h := range held {
		if h != nil && recordOverlap(ctx, a, client, h.ItemID, overlapSourceHold, h.Hold, "push refused: "+h.Error()) {
			n++
		}
	}
	return n
}

// splitHeldElsewhere partitions items by a hold probe already made: the items
// another location holds, and the rest in their original order.
func splitHeldElsewhere(items []json.RawMessage, held []*hosted.HeldError) (skipped []*hosted.HeldError, keep []json.RawMessage) {
	if len(items) == 0 || len(held) == 0 {
		return nil, items
	}
	byID := make(map[string]*hosted.HeldError, len(held))
	for _, h := range held {
		byID[h.ItemID] = h
	}
	keep = make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		if h, ok := byID[rawItemID(raw)]; ok {
			skipped = append(skipped, h)
			continue
		}
		keep = append(keep, raw)
	}
	return skipped, keep
}

// recordStoryConflicts persists what a pass found about story disagreements, for
// the session-start advisory to read (sty_6e5fc41c). It writes even when the
// pass failed: a refused pull is exactly the conflict to record. Each conflict
// not recorded before also becomes an overlap row on its story, naming who holds
// the hosted copy when the server says (best-effort, "unknown" otherwise).
func recordStoryConflicts(ctx context.Context, a *app.App, client *hosted.Client, project string, rep passReport) {
	global := config.GlobalDir()
	var known map[string]syncstate.StoryConflict
	if st, ok, err := syncstate.Load(global, a.RepoRoot); err == nil && ok {
		known = st.StoryConflicts
	}
	_ = syncstate.RecordStoryConflicts(global, a.RepoRoot, rep.Stories.Conflicts, rep.Stories.Cleared, time.Now().UTC())
	for _, id := range slices.Sorted(maps.Keys(rep.Stories.Conflicts)) {
		if _, seen := known[id]; seen {
			continue
		}
		var remote hosted.HoldState
		if client != nil && project != "" {
			if st, err := client.ItemHold(ctx, project, id); err == nil {
				remote = st
			}
		}
		recordOverlap(ctx, a, client, id, overlapSourceConflict, remote, rep.Stories.Conflicts[id])
	}
}
