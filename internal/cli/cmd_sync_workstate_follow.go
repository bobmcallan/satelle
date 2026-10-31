package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func newSyncWorkstateFollowCmd() *cobra.Command {
	var server string
	return &cobra.Command{
		Use:         "follow",
		Short:       "Follow the bound project's story chains live through the chain subscription",
		Annotations: needsStore(),
		Args:        cobra.NoArgs,
		Long: `follow subscribes, in hosted mode, to every event appended to the bound
project's stories and applies each as it arrives, resuming each story after the
last event this checkout holds. A story whose history the local store left on
the server is fetched whole when it changes recently, and any story the store
need not hold is trimmed again after its event. An event for a story this
checkout has no row of brings its row in, as the pull would.

It runs until it is stopped or the hosted server ends the stream. When the
hosted server has no subscription it exits non-zero, and satelled then keeps the
snapshot pass. It never pushes. A local-only checkout is a no-op.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncWorkstateFollow(cmd, server)
		},
	}
}

func runSyncWorkstateFollow(cmd *cobra.Command, serverArg string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if repoNotBound(out, a, "workstate follow") {
		return nil
	}
	if !verb.HostedMode(a.Config) {
		fmt.Fprintln(out, "workstate follow: not signed in — run \"satelle login\"; the local store is kept whole")
		return nil
	}
	optIn, err := workstateOptIn(a.Config)
	if err != nil {
		return err
	}
	if !optIn["ledger"] {
		fmt.Fprintln(out, "workstate follow: the ledger area is local, nothing to follow")
		return nil
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}
	conn, err := newHostedClient(cmd.Context(), server, a.RepoRoot).OpenChain(cmd.Context())
	if err != nil {
		return fmt.Errorf("open the story chain: %w", err)
	}
	defer conn.Close()
	f := &follower{a: a, w: cmd.ErrOrStderr(), conn: conn, server: server, project: project}
	cur, err := hosted.LoadWorkstateCursor(server, project, a.RepoRoot)
	if err != nil {
		return fmt.Errorf("load story heads: %w", err)
	}
	err = conn.SubscribeStories(cmd.Context(), project, cur.StoryHeads, f.apply)
	fmt.Fprintf(out, "Followed project %q: %d event(s) applied, %d stor(y/ies) landed, %d fetched, %d trimmed.\n", project, f.applied, f.landed, f.fetched, f.trimmed)
	switch {
	case errors.Is(err, hosted.ErrChainUnsupported):
		return fmt.Errorf("workstate follow: the server has no story chain subscription — keep the snapshot pass: %w", err)
	case errors.Is(err, context.Canceled):
		return nil
	}
	return err
}

// follower applies the events a subscription delivers, one story at a time.
type follower struct {
	a               *app.App
	w               io.Writer
	conn            *hosted.ChainConn
	server, project string

	applied, landed, fetched, trimmed int
}

// apply takes one delivered event. A story with no local row — one another
// machine created — has its row brought in by the pull scoped to it, which takes
// it to its chain head, so the event itself is not applied again (sty_dd594bb7).
// A story whose history is on the server is fetched whole when the story
// is now held, and otherwise left there. Any other story takes the event, its row
// is rebuilt from its chain, its head is saved, and it is trimmed when the store
// need not hold it.
func (f *follower) apply(storyID string, ev hosted.ChainEvent) error {
	ctx := context.Background()
	led := f.a.Store.Ledger
	row, err := f.a.Store.Stories.Get(ctx, storyID)
	if errors.Is(err, workitem.ErrNotFound) {
		f.land(ctx, storyID)
		return nil
	}
	if err != nil {
		return err
	}
	e, err := ledgerEntryOf(storyID, ev)
	if err != nil {
		return fmt.Errorf("decode %s of %s: %w", ev.ID, storyID, err)
	}
	now := time.Now().UTC()
	window, err := config.RecentWindow(f.a.Config)
	if err != nil {
		return err
	}
	held, err := verb.HeldStories(ctx, led, "", window, now)
	if err != nil {
		return err
	}
	remote, err := led.HasMark(ctx, storyID, ledger.MarkHistoryRemote)
	if err != nil {
		return err
	}
	if remote {
		if !held[storyID] && !e.CreatedAt.After(now.Add(-window)) {
			return nil
		}
		if _, err := fetchRemoteHistory(ctx, f.a, storyID); err != nil {
			return err
		}
		f.fetched++
	} else {
		if _, err := led.Upsert(ctx, e, now); err != nil {
			return fmt.Errorf("apply %s of %s: %w", ev.ID, storyID, err)
		}
		if err := hosted.SaveStoryHead(f.server, f.project, f.a.RepoRoot, storyID, ev.ID); err != nil {
			return fmt.Errorf("save the head of %s: %w", storyID, err)
		}
	}
	f.applied++
	if row.Kind == workitem.KindStory {
		if _, err := verb.RebuildRow(ctx, f.a.Store.Stories, led, row, now); err != nil {
			fmt.Fprintf(f.w, "workstate follow: rebuild %s from its chain: %v\n", storyID, err)
		}
	}
	if held, err = verb.HeldStories(ctx, led, "", window, now); err != nil {
		return err
	}
	n, err := verb.TrimStoryHistory(ctx, led, held, map[string]string{storyID: ev.ID})
	f.trimmed += n
	return err
}

// land brings in the row of a story this checkout has no row of through the pull
// scoped to it. A failure is reported and the event skipped, so one story the pull
// cannot land does not end the subscription.
func (f *follower) land(ctx context.Context, storyID string) {
	if err := pullWorkstate(ctx, f.a, io.Discard, f.w, f.server, false, false, false, storyID); err != nil {
		fmt.Fprintf(f.w, "workstate follow: bring in %s: %v\n", storyID, err)
		return
	}
	if _, err := f.a.Store.Stories.Get(ctx, storyID); err == nil {
		f.landed++
	}
}
