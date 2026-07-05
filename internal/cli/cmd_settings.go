package cli

// `satelle settings` — read/write the machine-wide GLOBAL config
// (~/.satelle/config.toml), decoupled from authentication (sty_432bdeb7).
// Configuring the hosted server is a CONFIG action, not an auth one: set it with
// `satelle settings server <url>` (no OAuth), then sign in via the local web UI or
// `satelle login`. Like login/whoami these commands never touch the local DB, so
// they carry no store annotation and work in a fresh clone.

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	settings := &cobra.Command{
		Use:   "settings",
		Short: "Read or write the machine-wide global config (~/.satelle/config.toml)",
	}

	var clear bool
	server := &cobra.Command{
		Use:   "server [url]",
		Short: "Show, set, or clear the global hosted-server URL (no login required)",
		Long: `server manages the machine-wide hosted-server binding in the global config,
without authenticating — configuring the server is separate from signing in.

  satelle settings server                 # print the current server
  satelle settings server <url>           # set it (then sign in via the UI or 'satelle login')
  satelle settings server --clear         # remove it

'satelle login --server <url>' also works — it sets the server AND authenticates.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsServer(cmd, args, clear)
		},
	}
	server.Flags().BoolVar(&clear, "clear", false, "Remove the configured global hosted server.")
	settings.AddCommand(server)
	register(settings)
}

func runSettingsServer(cmd *cobra.Command, args []string, clear bool) error {
	out := cmd.OutOrStdout()

	if clear {
		if len(args) > 0 {
			return fmt.Errorf("--clear takes no url argument")
		}
		if err := config.ClearGlobalHostedServer(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Cleared the global hosted server.")
		return nil
	}

	if len(args) == 1 {
		url := strings.TrimSpace(args[0])
		if err := config.SaveGlobalHostedServer(url); err != nil {
			return err
		}
		gc, _ := config.LoadGlobal()
		fmt.Fprintf(out, "Global hosted server set to %s.\nSign in with the web UI (Sign in) or `satelle login`.\n", gc.Hosted.ResolveServer())
		return nil
	}

	// No arg → print the current value.
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	if s := gc.Hosted.ResolveServer(); s != "" {
		fmt.Fprintln(out, s)
	} else {
		fmt.Fprintln(out, "no global hosted server configured — set one with `satelle settings server <url>`")
	}
	return nil
}
