package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
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
		st, err := h.client.Checkout(ctx, h.project, id)
		var held *hosted.HeldError
		switch {
		case errors.As(err, &held):
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, held.Hold.LocationID)
			_ = hosted.ClearPendingClaim(h.server, h.project, h.repoRoot, id)
			if collided == nil {
				collided = map[string]*hosted.HeldError{}
			}
			collided[id] = held
			label := ""
			if held.Hold.Label != "" {
				label = " (" + held.Hold.Label + ")"
			}
			fmt.Fprintf(out, "collision: %s was engaged here at %s without a hosted hold, but location %s%s holds it — its remaining rows are not sent; satelle story hold takeover %s\n",
				id, pending[id], held.Hold.LocationID, label, id)
		case err != nil:
			fmt.Fprintf(errw, "satelle: hold for %s stays pending: %v\n", id, err)
		default:
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, st.LocationID)
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
		if reg[id] != h.client.Location() {
			continue
		}
		_, err := h.client.Checkout(ctx, h.project, id)
		var taken *hosted.HeldError
		switch {
		case errors.As(err, &taken):
			_ = hosted.RecordHold(h.server, h.project, h.repoRoot, id, taken.Hold.LocationID)
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
		if reg[id] != h.client.Location() {
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

// withoutStories drops the ledger rows of the named stories.
func withoutStories(ledgerRows []json.RawMessage, drop map[string]*hosted.HeldError) []json.RawMessage {
	if len(drop) == 0 {
		return ledgerRows
	}
	keep := make([]json.RawMessage, 0, len(ledgerRows))
	for _, raw := range ledgerRows {
		if _, d := drop[rawLedgerStoryID(raw)]; !d {
			keep = append(keep, raw)
		}
	}
	return keep
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

// reportInFlight lists the pulled stories another location holds. The hosted
// copy of a story whose activity is held back keeps its pre-engage status, so
// the status says nothing about whether it is in flight: every story that is not
// terminal on its route is asked. The server offers no project-wide hold
// listing, so that is one lookup per such story; a failed lookup stops the
// probing with one warning and never fails the pull.
func reportInFlight(ctx context.Context, out, errw io.Writer, h holdSite, items []hosted.WorkstateItem) {
	self := h.client.Location()
	if self == "" {
		return
	}
	var lines []string
	for _, hi := range items {
		if hi.Kind != "story" {
			continue
		}
		it, err := parseWorkstateItem(hi)
		if err != nil {
			continue
		}
		if rest := verb.StoryRouteRest(ctx, it); rest.Known && rest.Terminal {
			continue
		}
		st, err := h.client.ItemHold(ctx, h.project, hi.ID)
		if errors.Is(err, hosted.ErrItemNotFound) {
			continue
		}
		if err != nil {
			fmt.Fprintf(errw, "satelle: in-flight check stopped at %s: %v\n", hi.ID, err)
			break
		}
		if st.LocationID == "" || st.LocationID == self {
			continue
		}
		lines = append(lines, "  "+describeHold(ctx, h, hi.ID, st))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(out, "%d stor(y/ies) in flight on other locations:\n", len(lines))
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}
