package verb

// Hosted mode and the local history cache (sty_0dc600e1). When the user is signed
// in and the repo bound, the server is the store of record and the local ledger is
// a cache: it holds full history only for the stories this machine engaged and the
// ones that changed within [sync] recent_window. Every other story keeps its row,
// and its history is fetched again when it is opened. Local only, nothing here
// runs and the full store stays.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// RepoBound is config.RepoBound: the repo names its hosted project explicitly.
func RepoBound(cfg config.Config) bool { return config.RepoBound(cfg) }

// HostedMode reports whether the server is the store of record for this repo: the
// user is signed in and the repo is bound.
func HostedMode(cfg config.Config) bool {
	return SignedIn() && RepoBound(cfg)
}

// MachineSet returns the stories this machine engaged: those with a KindWorkingOn
// row naming location. An empty location is this machine's own location as the
// location resolver names it. The row is read to choose what stays cached, never
// to refuse an engage.
func MachineSet(ctx context.Context, led *ledger.Store, location string) (map[string]bool, error) {
	if led == nil {
		return nil, fmt.Errorf("verb: machine set needs a ledger")
	}
	if location == "" && locationResolver != nil {
		location, _ = locationResolver()
	}
	out := map[string]bool{}
	err := led.ForEachKind(ctx, "", ledger.KindWorkingOn, func(e ledger.Entry) error {
		var p WorkingOnPayload
		if json.Unmarshal(e.Payload, &p) == nil && p.Location == location && e.StoryID != "" {
			out[e.StoryID] = true
		}
		return nil
	})
	return out, err
}

// HeldStories returns the stories whose full history the local store keeps in
// hosted mode: the machine set plus every story whose newest event was made
// after now - window.
func HeldStories(ctx context.Context, led *ledger.Store, location string, window time.Duration, now time.Time) (map[string]bool, error) {
	held, err := MachineSet(ctx, led, location)
	if err != nil {
		return nil, err
	}
	latest, err := led.LatestEventTimes(ctx)
	if err != nil {
		return nil, err
	}
	since := now.Add(-window)
	for id, at := range latest {
		if at.After(since) {
			held[id] = true
		}
	}
	return held, nil
}

// TrimStoryHistory drops the history of every story with events that is not in
// held, and returns how many it dropped. A story is dropped only when the server
// holds all of it: its chain ends at heads[id], the last event the server accepted
// from this checkout, and its row is not waiting on an Apply. Everything else is
// kept as it is.
func TrimStoryHistory(ctx context.Context, led *ledger.Store, held map[string]bool, heads map[string]string) (int, error) {
	if led == nil {
		return 0, fmt.Errorf("verb: trim needs a ledger")
	}
	ids, err := led.StoryIDs(ctx)
	if err != nil {
		return 0, err
	}
	pending, err := led.Marked(ctx, ledger.MarkItemPending)
	if err != nil {
		return 0, err
	}
	dropped := 0
	for _, id := range ids {
		if held[id] || pending[id] || heads[id] == "" {
			continue
		}
		ok, err := led.DropStoryHistory(ctx, id, heads[id])
		if err != nil {
			return dropped, err
		}
		if ok {
			dropped++
		}
	}
	return dropped, nil
}
