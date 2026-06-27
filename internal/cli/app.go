package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/reviewer"
	"github.com/bobmcallan/satelle/internal/verb"
)

// storeAnnotation marks a command as needing the local store. The root's
// persistent pre-run opens the bootstrap (config + db) only for these commands
// and closes it after — so `satelle version` / `--help` never create a db.
const storeAnnotation = "needs-store"

// appCtxKey carries the opened *app.App on the command context.
type appCtxKey struct{}

// needsStore returns a cobra annotations map flagging a store-backed command.
func needsStore() map[string]string { return map[string]string{storeAnnotation: "1"} }

// openAppForCmd opens the bootstrap and stashes it on the command's context.
// Called from the root's PersistentPreRunE for store-backed commands.
func openAppForCmd(cmd *cobra.Command) error {
	a, err := app.Open()
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	// Wire the opened stores into the verb registry — the single seam both the
	// CLI and the web server dispatch through. The CLI is one-shot, so wiring
	// the package globals per invocation is correct.
	verb.SetWorkItemStore(a.Store.Stories)
	verb.SetLedgerStore(a.Store.Ledger)
	verb.SetDocIndexStore(a.Store.DocIndex)
	// Mirror stories as portable markdown beside the per-repo database
	// (<data_dir>/stories); the store stays the source of truth.
	verb.SetStoryDir(filepath.Join(filepath.Dir(a.DBPath), "stories"))
	// Wire the isolated reviewer that gates status transitions. The agent CLI is
	// the install-time choice (global config); the gate is inert until a
	// workflow names a reviewer skill whose rubric is installed.
	if gc, gerr := config.LoadGlobal(); gerr == nil {
		if runner, rerr := agentcli.NewRunner(gc.Agent.ResolveCLI()); rerr == nil {
			rev := reviewer.New(runner, a.Store.DocIndex, a.RepoRoot, "")
			verb.SetTransitionGater(rev)
			// The summariser recaps gated transitions; inert until gating is active.
			verb.SetStepSummariser(rev)
			// Create-gating is opt-in per repo (satelle.toml [review] gate_create):
			// the rubric ships embedded, but enforcing it is the operator's choice.
			if a.Config.Review.GateCreate {
				verb.SetCreateReviewer(rev)
			}
		}
	}
	cmd.SetContext(context.WithValue(cmd.Context(), appCtxKey{}, a))
	return nil
}

// closeAppForCmd closes the bootstrap stashed on the command context, if any.
func closeAppForCmd(cmd *cobra.Command) {
	if a, ok := cmd.Context().Value(appCtxKey{}).(*app.App); ok && a != nil {
		_ = a.Close()
	}
}

// appFrom returns the opened *app.App from the command context. It is present
// for any command carrying the storeAnnotation (the pre-run opened it).
func appFrom(cmd *cobra.Command) (*app.App, error) {
	a, ok := cmd.Context().Value(appCtxKey{}).(*app.App)
	if !ok || a == nil {
		return nil, fmt.Errorf("internal: store not initialised for %q", cmd.CommandPath())
	}
	return a, nil
}

// gaterForCmd builds a reviewer.Gater over the opened store and the install-time
// agent CLI — the concrete reviewer used by the read paths (`satelle validate`,
// `satelle <object> create`) that need structure verdicts directly.
func gaterForCmd(cmd *cobra.Command) (*reviewer.Gater, *app.App, error) {
	a, err := appFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return nil, nil, err
	}
	runner, err := agentcli.NewRunner(gc.Agent.ResolveCLI())
	if err != nil {
		return nil, nil, fmt.Errorf("an agent CLI is required: %w", err)
	}
	return reviewer.New(runner, a.Store.DocIndex, a.RepoRoot, ""), a, nil
}
