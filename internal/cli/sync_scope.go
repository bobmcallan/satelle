package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/syncscope"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// syncAccountFor is the account a scope resolve asks: the hosted client for
// server. A variable so a test can stand in for the account service.
var syncAccountFor = func(ctx context.Context, server, repoRoot string) syncscope.Account {
	return newHostedClient(ctx, server, repoRoot)
}

// syncSignedIn reports whether this machine holds a credential for server.
var syncSignedIn = func(server string) bool {
	_, err := hosted.DefaultStore().Load(server)
	return err == nil
}

type scopeMemoKey struct{}

// resolveSyncScope is the CLI's one caller of syncscope.Resolve: the scope of
// this repo's explicit [sync] project binding for the signed-in account on
// server. One command resolves once — a composed sync's legs share the answer —
// and the answer is recorded for the offline checks (syncstate.RecordedScope).
// Unbound or signed out makes no hosted call.
func resolveSyncScope(cmd *cobra.Command, cfg config.Config, repoRoot, server string) (syncscope.Resolution, error) {
	binding := strings.TrimSpace(cfg.SyncProject())
	key := server + "\x00" + binding
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	memo, _ := ctx.Value(scopeMemoKey{}).(map[string]syncscope.Resolution)
	if res, ok := memo[key]; ok {
		return res, nil
	}
	in := syncscope.Input{Binding: binding}
	if binding != "" && server != "" && syncSignedIn(server) {
		in.SignedIn = true
		in.Account = syncAccountFor(ctx, server, repoRoot)
	}
	res, err := syncscope.Resolve(ctx, in)
	if err != nil {
		return res, err
	}
	_ = syncstate.RecordResolvedScope(config.GlobalDir(), repoRoot, res.Scope, binding, time.Now().UTC())
	if memo == nil {
		memo = map[string]syncscope.Resolution{}
		cmd.SetContext(context.WithValue(ctx, scopeMemoKey{}, memo))
	}
	memo[key] = res
	return res, nil
}
