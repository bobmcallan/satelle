package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// storyHoldCommands is `satelle story hold {checkout,release,takeover}`.
// Distinct from `satelle story seat release`, which frees a local engagement
// seat. Hold verbs talk to the hosted REST hold surface (sty_dec88606).
func storyHoldCommands() *cobra.Command {
	var server string
	hold := &cobra.Command{
		Use:   "hold",
		Short: "Checkout, release, or take over a hosted story (not the engagement seat)",
		Long: `Hosted story-hold: which location may work a canonical hosted story.

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
	takeover := &cobra.Command{
		Use:         "takeover <id>",
		Short:       "Move the hosted hold to this location",
		Long:        `Take over a story held by another location. Names the previous location and last_seen. Holds do not expire; takeover is the recovery path for a dead location.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryHoldTakeover(cmd, server, args[0])
		},
	}
	hold.AddCommand(checkout, release, takeover)
	return hold
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

func runStoryHoldCheckout(cmd *cobra.Command, serverArg, id string) error {
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	h, err := client.Checkout(cmd.Context(), project, id)
	if err != nil {
		return err
	}
	_ = hosted.RecordHold(resolveServer(serverArg), project, repoRoot, id, h.LocationID)
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

func runStoryHoldTakeover(cmd *cobra.Command, serverArg, id string) error {
	client, project, repoRoot, err := holdClient(cmd, serverArg)
	if err != nil {
		return err
	}
	res, err := client.TakeoverHold(cmd.Context(), project, id)
	if err != nil {
		return err
	}
	_ = hosted.RecordHold(resolveServer(serverArg), project, repoRoot, id, res.Hold.LocationID)
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
