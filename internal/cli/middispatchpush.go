// Mid-dispatch observability push (sty_752c4ef2 AC5): a running dispatch's
// agent, model and pid are recorded on its open transition, and a light
// engagement-only snapshot is pushed to the local serve mirror WHILE the
// dispatch runs, not only at command end — so the web story row's running
// indicator stays fresh during a long-running dispatch.
package cli

import (
	"context"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/verb"
)

// midDispatchPushBudget bounds one mid-dispatch push so a black-holed local
// serve endpoint cannot stall the dispatch's event loop for more than a moment.
// Shorter than uiDrainBudget: this may fire many times over one long dispatch,
// not once at command end.
var midDispatchPushBudget = 800 * time.Millisecond

// buildEngagementSnapshot is the lightest possible partition push: just the
// engagement rows, replacing ONLY the mirror's "engagement" partition (Kinds) —
// a mid-dispatch push never touches story/task/doc/ledger state.
func buildEngagementSnapshot(ctx context.Context, a *app.App) (*mirror.Snapshot, error) {
	rows, err := listEngagementsJSON(ctx, a)
	if err != nil {
		return nil, err
	}
	return &mirror.Snapshot{
		RepoKey: config.RepoKey(a.RepoRoot), Slug: partitionSlug(a),
		Engagements: rows, Kinds: []string{"engagement"},
	}, nil
}

// pushEngagementSnapshot posts an engagement-only snapshot to endpoint,
// best-effort, under midDispatchPushBudget. a/endpoint nil-checked so an
// unwired mirror (no `satelle serve`, or no global config) is silently inert.
func pushEngagementSnapshot(a *app.App, endpoint string) {
	if a == nil || endpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), midDispatchPushBudget)
	defer cancel()
	snap, err := buildEngagementSnapshot(ctx, a)
	if err != nil || snap == nil {
		return
	}
	_ = postUISnapshotContext(ctx, endpoint, snap)
}

// liveDispatch holds each item's latest dispatch event, as seen by this process.
// Only the process running a dispatch sees its events; the ledger records the
// dispatch once, not every throttled event.
var liveDispatch sync.Map // item id → agentstep.ActivityDetail

// latestDispatchEvent returns the latest event this process saw for itemID's
// dispatch by agent.
func latestDispatchEvent(itemID, agent string) (agentstep.ActivityDetail, bool) {
	v, ok := liveDispatch.Load(itemID)
	if !ok {
		return agentstep.ActivityDetail{}, false
	}
	d := v.(agentstep.ActivityDetail)
	return d, d.Agent == agent
}

// activityDetailSink is the SetActivityDetail callback body, extracted so it
// is directly testable without booting the full CLI app. It records the
// dispatch on the item's open transition, then pushes it to the local mirror —
// both best-effort; observability must never fail a dispatch.
func activityDetailSink(a *app.App, endpoint, itemID string, d agentstep.ActivityDetail) {
	if itemID == "" {
		return
	}
	liveDispatch.Store(itemID, d)
	verb.RecordTransitionActivity(context.Background(), itemID, ledger.TransitionActivityPayload{
		Agent: d.Agent, Model: d.Model, Pid: d.Pid,
	})
	pushEngagementSnapshot(a, endpoint)
}
