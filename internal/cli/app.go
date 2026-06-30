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
	"github.com/bobmcallan/satelle/internal/workitem"
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
	// The stories dir (<data_dir>/stories) holds per-story ATTACHMENTS only — the
	// database is the sole story store (the markdown mirror was removed, sty_fa1e02e1).
	verb.SetStoryDir(filepath.Join(filepath.Dir(a.DBPath), "stories"))
	// Wire the isolated reviewer that gates status transitions. The agent CLI is
	// the install-time choice (global config); the gate is inert until a
	// workflow names a reviewer skill whose rubric is installed.
	if gc, gerr := config.LoadGlobal(); gerr == nil {
		if runner, rerr := agentcli.NewRunner(gc.Agent.ResolveCLI()); rerr == nil {
			rev := reviewer.New(runner, a.Store.DocIndex, a.RepoRoot, "")
			applyAgentGrants(rev, a)
			rev.SetChildrenResolver(childrenResolver(a))
			verb.SetTransitionGater(rev)
			// Stamp the governing workflow on every story at create — independent of
			// create-gating (sty_3800ac23).
			verb.SetWorkflowResolver(rev)
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
	rev := reviewer.New(runner, a.Store.DocIndex, a.RepoRoot, "")
	applyAgentGrants(rev, a)
	return rev, a, nil
}

// childrenResolver lists a parent's child stories (id + status) from the DB, for
// the container close gate's payload — so a parent/epic close is judged from the
// database, never an on-disk story mirror (sty_fa1e02e1).
func childrenResolver(a *app.App) func(ctx context.Context, parentID string) []reviewer.ChildState {
	return func(ctx context.Context, parentID string) []reviewer.ChildState {
		if parentID == "" {
			return nil
		}
		kids, err := a.Store.Stories.List(ctx, workitem.ListFilter{ParentID: parentID})
		if err != nil {
			return nil
		}
		out := make([]reviewer.ChildState, 0, len(kids))
		for _, k := range kids {
			out = append(out, reviewer.ChildState{ID: k.ID, Status: k.Status})
		}
		return out
	}
}

// skillResolver returns a predicate reporting whether a skill name resolves in
// the substrate (project ∪ embedded), for the deterministic workflow structure
// check's executor-skill actionability. Used by `satelle validate` and `index`.
func skillResolver(a *app.App) func(skill string) bool {
	return func(skill string) bool {
		_, err := a.Store.DocIndex.Get(context.Background(), "skills", skill)
		return err == nil
	}
}

// applyAgentGrants resolves the agents layer (.satelle/agents.toml) and binds the
// reviewer's tool grant onto the gater. An absent file yields today's read-only
// default, so behaviour is unchanged unless a repo authors agents.toml.
func applyAgentGrants(rev *reviewer.Gater, a *app.App) {
	if agents, err := config.LoadAgents(filepath.Dir(a.DBPath)); err == nil {
		rb := agents.ReviewerBinding()
		rev.SetReviewerTools(rb.Tools)
		rev.SetReviewerModel(rb.Model)
		// Select the reviewer's agent CLI from the agents-layer harness binding
		// (default claude). An unset/in-loop/unresolvable harness keeps the global
		// [agent] cli configured at construction.
		if r, rerr := agentcli.RunnerFromHarness(rb.Harness); rerr == nil {
			rev.SetRunner(r)
		}
	}
}
