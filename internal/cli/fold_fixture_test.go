package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A story's row is a cache of the fold of its ledger chain (sty_d84be8bc), so a
// fixture that writes a story's status straight to the row must also put the
// transition on the chain, and bring the cached park origin to what the fold says
// — what every verb that moves a story does. These two helpers are that.

// settleRow rebuilds one story's cached row from its chain, with the verb layer
// reading the repo's indexed route as a command run does.
func settleRow(t *testing.T, a *app.App, id string) {
	t.Helper()
	withVerbWiring(t)
	verb.SetDocIndexStore(a.Store.DocIndex)
	ctx := context.Background()
	row, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verb.RebuildRow(ctx, a.Store.Stories, a.Store.Ledger, row, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// moveRow moves a story to status the way a transition does: the row, and the
// status_transition event the row is a cache of.
func moveRow(t *testing.T, a *app.App, id, to string) {
	t.Helper()
	ctx, now := context.Background(), time.Now().UTC()
	row, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"from": row.Status, "to": to})
	if _, err := a.Store.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: id, Kind: ledger.KindStatusTransition, Actor: "executor", Body: row.Status + " → " + to, Payload: payload,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Stories.SetStatus(ctx, id, to, now); err != nil {
		t.Fatal(err)
	}
	settleRow(t, a, id)
}
