package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/verb"
)

func newSyncWorkstateRechainCmd() *cobra.Command {
	var server string
	var dryRun bool
	cmd := &cobra.Command{
		Use:         "rechain [story-id...]",
		Short:       "Put a story forked by an earlier pull onto its hosted chain",
		Annotations: needsStore(),
		Args:        cobra.ArbitraryArgs,
		Long: `rechain repairs a story whose local chain is only a genesis an earlier pull
seeded from the story row (a workflow_version and a definition_snapshot) while the
hosted project holds its real events, so every push of it is refused as moved.

It removes exactly the seeded events, takes the hosted chain in their place,
rebuilds the story row and clears its recorded conflict. It never pushes.

A story with any other event the hosted chain lacks is local work: it is refused
by name and left untouched. With no ids it takes every story recorded as in
conflict.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstateRechain(cmd, server, dryRun, args)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List the stories that would be repaired without changing anything.")
	return cmd
}

func runSyncWorkstateRechain(cmd *cobra.Command, serverArg string, dryRun bool, ids []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	srv := resolveServer(serverArg)
	if srv == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		st, _, lerr := syncstate.Load(config.GlobalDir(), a.RepoRoot)
		if lerr != nil {
			return lerr
		}
		ids = slices.Sorted(maps.Keys(st.StoryConflicts))
		if len(ids) == 0 {
			fmt.Fprintln(out, "No story is recorded as in conflict; name story ids to rechain.")
			return nil
		}
	}
	chain := newChainPull(newHostedClient(ctx, srv, a.RepoRoot), srv, project, a.RepoRoot, cmd.ErrOrStderr())
	defer chain.close()
	rd, err := chain.reader(ctx)
	if err != nil {
		return err
	}
	failed := 0
	for i, id := range ids {
		if i > 0 && !pause(ctx, appendPace) {
			return ctx.Err()
		}
		if err := rechainStory(ctx, out, a, chain, rd, id, dryRun); err != nil {
			fmt.Fprintf(out, "refused %s: %v\n", id, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d stor(y/ies) not rechained — see above", failed)
	}
	return nil
}

// rechainStory repairs one story and reports it on out. A story that is not a
// seeded fork of its hosted chain is returned as an error, untouched.
func rechainStory(ctx context.Context, out io.Writer, a *app.App, chain *chainPull, rd chainReader, id string, dryRun bool) error {
	local, err := a.Store.Ledger.StoryChain(ctx, id)
	if err != nil {
		return fmt.Errorf("read the local chain: %w", err)
	}
	if len(local) == 0 {
		return fmt.Errorf("no local events")
	}
	events, latest, err := rd.ReadStory(ctx, chain.project, id, "")
	if err != nil {
		return fmt.Errorf("read the hosted chain: %w", err)
	}
	if latest == "" {
		return fmt.Errorf("the hosted project holds no events of it")
	}
	held, onHosted := map[string]bool{}, map[string]bool{}
	for _, e := range local {
		held[e.ID] = true
	}
	taking := 0
	for _, e := range events {
		onHosted[e.ID] = true
		if !held[e.ID] {
			taking++
		}
	}
	localOnly := map[string]bool{}
	for _, e := range local {
		if !onHosted[e.ID] {
			localOnly[e.ID] = true
		}
	}
	if len(localOnly) == 0 {
		fmt.Fprintf(out, "nothing to rechain %s: every local event is on the hosted chain\n", id)
		return nil
	}
	order, err := chainOrder(local)
	if err != nil {
		return err
	}
	seeded, ok := storyfold.SeededEvents(order, localOnly)
	if !ok {
		var named []string
		for _, e := range order {
			if localOnly[e.ID] {
				named = append(named, fmt.Sprintf("%s (%s)", e.ID, e.Kind))
			}
		}
		return fmt.Errorf("local events the hosted chain lacks are not only a seeded genesis: %s", strings.Join(named, ", "))
	}
	if dryRun {
		fmt.Fprintf(out, "would rechain %s: remove %d seeded event(s), take %d hosted event(s)\n", id, len(seeded), taking)
		return nil
	}
	if err := a.Store.Ledger.RemoveEvents(ctx, id, seeded, storyfold.IsSeededEvent); err != nil {
		return err
	}
	now := time.Now().UTC()
	took, err := chain.materializeStory(ctx, a, rd, id, nil, now)
	if err != nil {
		return err
	}
	if !took {
		return fmt.Errorf("the hosted chain could not be taken after removing the seeded events")
	}
	row, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read the story row: %w", err)
	}
	if _, err := verb.RebuildRow(ctx, a.Store.Stories, a.Store.Ledger, row, now); err != nil {
		return fmt.Errorf("rebuild the story row from its chain: %w", err)
	}
	chain.flush()
	_ = syncstate.RecordStoryConflicts(config.GlobalDir(), a.RepoRoot, nil, []string{id}, now)
	fmt.Fprintf(out, "rechained %s: removed %d seeded event(s), took %d hosted event(s)\n", id, len(seeded), taking)
	return nil
}
