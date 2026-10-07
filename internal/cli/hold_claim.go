package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
)

// hostedHoldClaimer is the verb.SetHoldClaimer func for a bound repo
// (sty_52eb8c2f): an engaging move checks the story out on the server before it
// takes the engagement lease. The server's checkout is the arbiter — one atomic
// call that grants the hold or names the holder — so two machines engaging the
// same story cannot both pass. Unwired when no server or no bound project.
//
// A server that cannot be reached, or that has never seen the story, does not
// stop the engage: the claim is recorded as pending and the next work-state push
// places it (reconcilePendingClaims). A lost race or a hold held elsewhere comes
// back as HeldElsewhere. Only a held-here answer is cached for the process, so a
// multi-step engage does not repeat the call.
func hostedHoldClaimer(a *app.App) func(ctx context.Context, itemID string) (verb.HoldInfo, error) {
	var mu sync.Mutex
	cache := map[string]verb.HoldInfo{}
	return func(ctx context.Context, itemID string) (verb.HoldInfo, error) {
		server := config.ResolveHostedServer(a.Config)
		project := a.Config.SyncProject()
		if server == "" || project == "" {
			return verb.HoldInfo{}, nil
		}
		mu.Lock()
		h, ok := cache[itemID]
		mu.Unlock()
		if ok {
			return h, nil
		}
		c := newHostedClient(ctx, server, a.RepoRoot)
		if c.Location() == "" {
			return verb.HoldInfo{}, nil
		}
		st, err := c.Checkout(ctx, project, itemID)
		var held *hosted.HeldError
		switch {
		case errors.As(err, &held):
			return verb.HoldInfo{
				Holder:        held.Hold.LocationID,
				HolderLabel:   held.Hold.Label,
				LastSeen:      held.Hold.LastSeenAt,
				HeldElsewhere: true,
			}, nil
		case err != nil:
			if rerr := hosted.RecordPendingClaim(server, project, a.RepoRoot, itemID, time.Now()); rerr != nil {
				return verb.HoldInfo{}, fmt.Errorf("%w (and the pending claim could not be recorded: %v): %v", verb.ErrHoldPending, rerr, err)
			}
			return verb.HoldInfo{}, fmt.Errorf("%w: %v", verb.ErrHoldPending, err)
		}
		_ = hosted.RecordHold(server, project, a.RepoRoot, itemID, st.LocationID)
		_ = hosted.ClearPendingClaim(server, project, a.RepoRoot, itemID)
		info := verb.HoldInfo{Holder: st.LocationID, HolderLabel: st.Label, LastSeen: st.LastSeenAt}
		mu.Lock()
		cache[itemID] = info
		mu.Unlock()
		return info, nil
	}
}
