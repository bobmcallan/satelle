package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// storyHoldCommands is `satelle story hold {checkout,release,takeover}`.
// Distinct from `satelle story seat release`, which frees a local engagement
// seat. Hold verbs talk to the hosted REST hold surface (sty_dec88606).
func storyHoldCommands() *cobra.Command {
	var server string
	hold := &cobra.Command{
		Use:   "hold",
		Short: "Checkout, release, take over, or show a hosted story's hold (not the engagement seat)",
		Long: `Hosted story-hold: which location may work a canonical hosted story.

Engaging a story claims its hold for you (checkout) before the engagement lease,
and a machine that cannot reach the server engages anyway and claims at its next
work-state push. satelle story hold checkout is the manual form of that claim.

This is not the engagement seat. satelle story seat / satelle story seat release
free a LOCAL lease in this working tree. satelle story hold release drops the
hosted hold so another location can check it out.

Requires a bound hosted project (satelle project bind). Unbound repos keep
fully-local stories; hold commands refuse rather than dial.`,
	}
	hold.PersistentFlags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")

	checkout := &cobra.Command{
		Use:         "checkout <id>",
		Short:       "Acquire the hosted hold for this location",
		Long:        `Acquire the hosted hold for this checkout. Idempotent if already held here. The story is then engageable at this location.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryHoldCheckout(cmd, server, args[0])
		},
	}
	release := &cobra.Command{
		Use:   "release <id>",
		Short: "Drop the hosted hold (holder-owner only)",
		Long: `Release the hosted hold so no location holds the story. Holder-owner only.

This is not satelle story seat release (that frees a local engagement seat).
List/get still work; engaging requires checkout again.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryHoldRelease(cmd, server, args[0])
		},
	}
	var force bool
	var reason string
	takeover := &cobra.Command{
		Use:   "takeover <id>",
		Short: "Move the hosted hold to this location, once the holder is out of the way and the story is in step",
		Long: `Take over a story held by another location. The hold moves only when the holder
is out of the way (released, finished, or silent past [sync] hold_stale_after),
this machine's copy of the status agrees with the hosted copy, and the commits
the story's ledger claims are in this repository. A refused takeover names each
failing check and its fix. --force --reason <text> is the only override, and is
recorded in the story's ledger. See: satelle help story-hold.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryHoldTakeover(cmd, server, args[0], force, reason)
		},
	}
	takeover.Flags().BoolVar(&force, "force", false, "Take over although a check failed (needs --reason; recorded in the story's ledger).")
	takeover.Flags().StringVar(&reason, "reason", "", "Why the takeover overrides the failed checks (only with --force).")
	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show who holds a story on the hosted server, and since when",
		Long: `Show the hosted hold of one story: the holder location and label, and either
"since <t>" (when the story's checkout log has a checkout or takeover entry for
that holder) or "last seen <t>" (when it has not — last-seen is never presented
as since). Prints "unheld" when no location holds it.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryHoldShow(cmd, server, args[0])
		},
	}
	hold.AddCommand(checkout, release, takeover, show)
	return hold
}

func runStoryHoldShow(cmd *cobra.Command, serverArg, id string) error {
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	st, hi, err := client.ItemHold(cmd.Context(), project, id)
	switch {
	case errors.Is(err, hosted.ErrItemNotFound):
		fmt.Fprintf(cmd.OutOrStdout(), "%s unheld (not on the hosted server yet)\n", id)
		return nil
	case err != nil:
		return err
	case st.LocationID == "":
		fmt.Fprintf(cmd.OutOrStdout(), "%s unheld\n", id)
		return nil
	case st.LocationID != client.Location() && hostedItemTerminal(cmd.Context(), hi):
		fmt.Fprintf(cmd.OutOrStdout(), "%s unheld (terminal)\n", id)
		return nil
	}
	h := holdSite{client: client, server: resolveServer(serverArg), project: project, repoRoot: repoRoot}
	line := describeHold(cmd.Context(), h, id, st)
	if client.Location() != "" && st.LocationID == client.Location() {
		line += " (this location)"
	}
	fmt.Fprintln(cmd.OutOrStdout(), line)
	return nil
}

func holdClient(cmd *cobra.Command, serverArg string) (client *hosted.Client, project, repoRoot string, err error) {
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		return nil, "", "", err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return nil, "", "", fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	if cfg.SyncProject() == "" {
		return nil, "", "", fmt.Errorf("no hosted project bound — story hold applies to bound repos only")
	}
	project, err = resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return nil, "", "", err
	}
	return newHostedClient(cmd.Context(), server, repoRoot), project, repoRoot, nil
}

// errExecutionNotHoldable refuses a hold verb that would place a hold on an
// execution. Executions are append-only; there is nothing to contend for.
func errExecutionNotHoldable(id string) error {
	return fmt.Errorf("%s is an execution; executions are append-only and are never held", id)
}

func runStoryHoldCheckout(cmd *cobra.Command, serverArg, id string) error {
	if !workitem.Holdable(id) {
		return errExecutionNotHoldable(id)
	}
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	h, err := client.Checkout(cmd.Context(), project, id)
	if err != nil {
		return err
	}
	_ = hosted.RecordHold(resolveServer(serverArg), project, repoRoot, id, hosted.HoldRecordFrom(h, time.Now()))
	fmt.Fprintf(cmd.OutOrStdout(), "checked out %s — held here (%s)\n", id, h.LocationID)
	return nil
}

func runStoryHoldRelease(cmd *cobra.Command, serverArg, id string) error {
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	if err := client.ReleaseHold(cmd.Context(), project, id); err != nil {
		return err
	}
	_ = hosted.ForgetHold(resolveServer(serverArg), project, repoRoot, id)
	fmt.Fprintf(cmd.OutOrStdout(), "released %s — unheld (not satelle story seat release)\n", id)
	return nil
}

func runStoryHoldTakeover(cmd *cobra.Command, serverArg, id string, force bool, reason string) error {
	if !workitem.Holdable(id) {
		return errExecutionNotHoldable(id)
	}
	reason = strings.TrimSpace(reason)
	if force && reason == "" {
		return fmt.Errorf("--force needs --reason <text>: the override and its reason are recorded in the story's ledger")
	}
	if !force && reason != "" {
		return fmt.Errorf("--reason only goes with --force")
	}
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	failed, err := assessTakeover(cmd.Context(), a, cmd.ErrOrStderr(), client, server, project, id, time.Now())
	if err != nil {
		return err
	}
	if len(failed) > 0 && !force {
		var b strings.Builder
		fmt.Fprintf(&b, "takeover of %s refused — %d check(s) failed:", id, len(failed))
		for _, c := range failed {
			fmt.Fprintf(&b, "\n  - %s", c)
		}
		fmt.Fprintf(&b, "\n--force --reason <text> overrides, and records the reason and these checks in the story's ledger")
		return errors.New(b.String())
	}
	res, err := client.TakeoverHold(cmd.Context(), project, id)
	if err != nil {
		return err
	}
	_ = hosted.RecordHold(server, project, repoRoot, id, hosted.HoldRecordFrom(res.Hold, time.Now()))
	if len(failed) > 0 {
		if err := recordForcedTakeover(cmd.Context(), a, id, reason, failed); err != nil {
			return fmt.Errorf("hold moved here, but recording the override in the ledger failed: %w", err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "forced past %d failed check(s); recorded in the ledger of %s\n", len(failed), id)
	}
	if strings.TrimSpace(res.Previous.LocationID) == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "hold moved here (%s) — was unheld\n", res.Hold.LocationID)
		return nil
	}
	seen := hosted.FormatLastSeen(res.Previous.LastSeenAt)
	if seen == "" {
		seen = "unknown"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "hold moved here from %s (last seen %s)\n", res.Previous.LocationID, seen)
	return nil
}
