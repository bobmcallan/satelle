package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The hosted hold is the marker that tells another machine a story is in flight
// here (sty_52eb8c2f). Engaging claims it (hold_claim.go); these are the other
// ends of its life: placing the claims an offline engage left pending, keeping
// the marker fresh while the story's rows are held back, releasing it once the
// story is published at rest, and showing other machines' markers after a pull.
// None of them fails a push or a pull — the server stays the authority, and the
// next run tries again.

// holdSite is the (server, project, repo) a hold lives under.
type holdSite struct {
	client                    *hosted.Client
	server, project, repoRoot string
}

// reconcilePendingClaims places the hold for every story engaged here while the
// server could not be asked. Granted: it becomes a normal hold. Held by another
// location: a collision line names the holder and the takeover command, and the
// story is returned so its remaining rows are not sent. Unreachable: it stays
// pending for the next push.
func reconcilePendingClaims(ctx context.Context, out, errw io.Writer, h holdSite) map[string]*hosted.HeldError {
	pending, err := hosted.PendingClaims(h.server, h.project, h.repoRoot)
	if err != nil || len(pending) == 0 || h.client.Location() == "" {
		return nil
	}
	var collided map[string]*hosted.HeldError
	for _, id := range slices.Sorted(maps.Keys(pending)) {
		if !workitem.Holdable(id) {
			// A claim recorded before executions stopped being held: drop it
			// rather than place a fresh server hold.
			_ = hosted.ClearPendingClaim(h.server, h.project, h.repoRoot, id)
			continue
		}
		st, err := h.client.Checkout(ctx, h.project, id)
		var held *hosted.HeldError
		switch {
		case errors.As(err, &held):
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, hosted.HoldRecordFrom(held.Hold, time.Now()))
			_ = hosted.ClearPendingClaim(h.server, h.project, h.repoRoot, id)
			if collided == nil {
				collided = map[string]*hosted.HeldError{}
			}
			collided[id] = held
			label := ""
			if held.Hold.Label != "" {
				label = " (" + held.Hold.Label + ")"
			}
			fmt.Fprintf(out, "collision: %s was engaged here at %s without a hosted hold, but location %s%s holds it — its record is not sent until takeover, its ledger entries are; satelle story hold takeover %s\n",
				id, pending[id], held.Hold.LocationID, label, id)
		case err != nil:
			fmt.Fprintf(errw, "satelle: hold for %s stays pending: %v\n", id, err)
		default:
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, hosted.HoldRecordFrom(st, time.Now()))
			_ = hosted.ClearPendingClaim(h.server, h.project, h.repoRoot, id)
		}
	}
	return collided
}

// settlePendingClaims reconciles pending claims on a push that has no rows to
// send. It dials only when a claim is pending.
func settlePendingClaims(ctx context.Context, out, errw io.Writer, a *app.App, server, project string) {
	if pending, err := hosted.PendingClaims(server, project, a.RepoRoot); err != nil || len(pending) == 0 {
		return
	}
	h := holdSite{client: newHostedClient(ctx, server, a.RepoRoot), server: server, project: project, repoRoot: a.RepoRoot}
	reconcilePendingClaims(ctx, out, errw, h)
}

// refreshHeldBack re-checks-out the hold of each story held here whose rows the
// push is holding back, so the marker's last-seen keeps moving while the story's
// activity stays local. It sends nothing of the story's.
func refreshHeldBack(ctx context.Context, out, errw io.Writer, h holdSite, held map[string]string) {
	reg, err := hosted.LoadHolds(h.server, h.project, h.repoRoot)
	if err != nil || h.client.Location() == "" {
		return
	}
	for _, id := range slices.Sorted(maps.Keys(held)) {
		if reg[id].Location != h.client.Location() {
			continue
		}
		_, err := h.client.Checkout(ctx, h.project, id)
		var taken *hosted.HeldError
		switch {
		case errors.As(err, &taken):
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, hosted.HoldRecordFrom(taken.Hold, time.Now()))
			fmt.Fprintf(out, "hold lost: %s\n", taken.Error())
		case err != nil:
			fmt.Fprintf(errw, "satelle: hold refresh for %s: %v\n", id, err)
		}
	}
}

// releaseAtRest releases the hosted hold of each story whose activity this push
// published, when the story sits in a terminal or parked state of its governing
// route and is held here. A story still held back never reaches it, so its
// marker stays. It returns the ids released.
func releaseAtRest(ctx context.Context, errw io.Writer, a *app.App, h holdSite, published map[string]bool) []string {
	reg, err := hosted.LoadHolds(h.server, h.project, h.repoRoot)
	if err != nil || h.client.Location() == "" {
		return nil
	}
	var released []string
	for _, id := range slices.Sorted(maps.Keys(published)) {
		if reg[id].Location != h.client.Location() {
			continue
		}
		it, err := a.Store.Stories.Get(ctx, id)
		if err != nil {
			continue
		}
		rest := verb.StoryRouteRest(ctx, it)
		if !rest.Known || !(rest.Terminal || rest.Parked) {
			continue
		}
		if err := h.client.ReleaseHold(ctx, h.project, id); err != nil {
			fmt.Fprintf(errw, "satelle: hold release for %s: %v\n", id, err)
			continue
		}
		_ = hosted.ForgetHold(h.server, h.project, h.repoRoot, id)
		released = append(released, id)
	}
	return released
}

// publishedStoryIDs names every story an ingest carried a row for.
func publishedStoryIDs(items, ledgerRows []json.RawMessage) map[string]bool {
	ids := map[string]bool{}
	for _, raw := range items {
		if id := rawItemID(raw); id != "" {
			ids[id] = true
		}
	}
	for _, raw := range ledgerRows {
		if id := rawLedgerStoryID(raw); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func rawLedgerStoryID(raw json.RawMessage) string {
	var w struct {
		StoryID string `json:"story_id"`
	}
	_ = json.Unmarshal(raw, &w)
	return w.StoryID
}

// holdTimeLabel says how long a hold has stood. "since" is claimed only when the
// story's checkout log has a checkout or takeover entry for the current holder;
// without one the hold's last-seen is shown, labelled as that and nothing more.
func holdTimeLabel(ctx context.Context, h holdSite, id string, st hosted.HoldState) string {
	if log, err := h.client.HoldLog(ctx, h.project, id); err == nil {
		if t, ok := hosted.HoldSince(log, st.LocationID); ok {
			return "since " + hosted.FormatLastSeen(t)
		}
	}
	seen := hosted.FormatLastSeen(st.LastSeenAt)
	if seen == "" {
		seen = "unknown"
	}
	return "last seen " + seen
}

// describeHold is the one line for a story held by st.
func describeHold(ctx context.Context, h holdSite, id string, st hosted.HoldState) string {
	label := ""
	if st.Label != "" {
		label = " (" + st.Label + ")"
	}
	return fmt.Sprintf("%s held by location %s%s, %s", id, st.LocationID, label, holdTimeLabel(ctx, h, id, st))
}

// holdLookupBudget bounds the hold lookups of one pass, so a slow server cannot
// hold a satelled snapshot past its own deadline. Stories not reached are asked
// again by the next pass.
const holdLookupBudget = 60 * time.Second

// holdLookupIDs names the stories a pass asks the server about. The hosted copy
// of a story whose activity is held back keeps its pre-engage status, and a hold
// taken or released does not change the story row at all, so what a pass
// received says nothing about holds: every local story is a candidate, plus any
// id the registry already has held by another location (so a release is always
// re-checked), or just the named ids for a scoped pass. A story terminal on its
// route is returned in atRest: it needs no lookup, and any hold recorded for it
// is stale. The server offers no project-wide hold listing, so ask costs one
// lookup per story.
func holdLookupIDs(ctx context.Context, a *app.App, rep passReport, scope []string, recorded map[string]hosted.HoldRecord, self string) (ask, atRest []string) {
	ids := map[string]bool{}
	if len(scope) > 0 {
		for _, id := range scope {
			ids[id] = true
		}
	} else {
		for _, hi := range rep.Hosted {
			if hi.Kind == string(workitem.KindStory) {
				ids[hi.ID] = true
			}
		}
		if local, err := a.Store.Stories.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000}); err == nil {
			for _, it := range local {
				ids[it.ID] = true
			}
		}
		for id, rec := range recorded {
			if rec.Location != self {
				ids[id] = true
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		if !workitem.Holdable(id) {
			continue
		}
		if it, err := a.Store.Stories.Get(ctx, id); err == nil {
			if rest := verb.StoryRouteRest(ctx, it); rest.Known && rest.Terminal {
				atRest = append(atRest, id)
				continue
			}
		}
		ask = append(ask, id)
	}
	return ask, atRest
}

// lookupHolds asks the server who holds each id. checked is every id the server
// answered for, held or not; a lookup that fails stops the probing and returns
// the ids reached so far with the error, so the caller never treats an id it did
// not reach as released.
func lookupHolds(ctx context.Context, client *hosted.Client, project string, ids []string) (found map[string]hosted.HoldState, checked []string, err error) {
	found = map[string]hosted.HoldState{}
	for _, id := range ids {
		st, lerr := client.ItemHold(ctx, project, id)
		if lerr != nil && !errors.Is(lerr, hosted.ErrItemNotFound) {
			return found, checked, fmt.Errorf("%s: %w", id, lerr)
		}
		checked = append(checked, id)
		if st.LocationID != "" {
			found[id] = st
		}
	}
	return found, checked, nil
}

// syncHolds is the hold half of a work-state pass (sty_6e5fc41c): it looks up the
// pass's candidate stories, records every hold another location has on one in
// the local registry with its label, last-seen and the time it was checked, and
// clears the recorded holds that were released or whose story finished. A failed
// lookup never fails the pass; it stops with one warning and records only what it
// reached. It returns the holds found, for a verb that prints them.
func syncHolds(ctx context.Context, errw io.Writer, a *app.App, h holdSite, rep passReport, scope []string) map[string]hosted.HoldState {
	self := h.client.Location()
	if self == "" {
		return nil
	}
	recorded, _ := hosted.LoadHolds(h.server, h.project, h.repoRoot)
	ask, atRest := holdLookupIDs(ctx, a, rep, scope, recorded, self)
	lctx, cancel := context.WithTimeout(ctx, holdLookupBudget)
	defer cancel()
	found, checked, err := lookupHolds(lctx, h.client, h.project, ask)
	if err != nil {
		fmt.Fprintf(errw, "satelle: in-flight check stopped at %v\n", err)
	}
	now := time.Now().UTC()
	records := make(map[string]hosted.HoldRecord, len(found))
	for id, st := range found {
		records[id] = hosted.HoldRecordFrom(st, now)
	}
	if rerr := hosted.ReconcileHolds(h.server, h.project, h.repoRoot, self, records, append(checked, atRest...)); rerr != nil {
		fmt.Fprintf(errw, "satelle: record holds: %v\n", rerr)
	}
	return found
}

// reportInFlight lists the stories another location holds.
func reportInFlight(ctx context.Context, out io.Writer, h holdSite, found map[string]hosted.HoldState) {
	self := h.client.Location()
	var lines []string
	for _, id := range slices.Sorted(maps.Keys(found)) {
		if st := found[id]; st.LocationID != self {
			lines = append(lines, "  "+describeHold(ctx, h, id, st))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(out, "%d stor(y/ies) in flight on other locations:\n", len(lines))
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}
