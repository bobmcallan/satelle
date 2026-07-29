// `satelle agents install|remove` provisions satelle-owned launcher scripts
// under $SATELLE_HOME/agents/bin/ (sty_aa726901). Distinct from `satelle agent`
// (singular: select/validate the headless CLI).
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentinstall"
	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	agents := &cobra.Command{
		Use:   "agents",
		Short: "Install or remove satelle-owned agent launcher scripts (claude | grok | codex | all)",
		Long: `agents install and remove manage satelle-owned launcher scripts under
$SATELLE_HOME/agents/bin/ (e.g. satelle-codex → npx Codex ACP adapter).

They do not install third-party packages globally, do not change
~/.satelle/config.toml [agent] cli, and do not edit any repo's agents.toml —
so a Codex install path can be verified without changing the default reviewer.

For selecting or validating the headless agent CLI, use satelle agent (singular):
  satelle agent show | set | detect | validate`,
	}

	install := &cobra.Command{
		Use:   "install <claude|grok|codex|all>",
		Short: "Install satelle-owned launcher scripts (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := config.GlobalDir()
			rs, err := agentinstall.Install(home, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range rs {
				fmt.Fprintf(out, "%s %s → %s", r.Action, r.Name, r.Path)
				if r.Note != "" {
					fmt.Fprintf(out, " (%s)", r.Note)
				}
				fmt.Fprintln(out)
				if r.Action == "created" || r.Action == "updated" || r.Action == "unchanged" {
					if snip := agentinstall.BindingSnippet(r.Name, r.Path); snip != "" {
						fmt.Fprintln(out, "  sample binding (paste into agents.toml; does not change default reviewer):")
						for _, line := range strings.Split(strings.TrimRight(snip, "\n"), "\n") {
							fmt.Fprintf(out, "  %s\n", line)
						}
					}
				}
			}
			fmt.Fprintln(out, "No default reviewer or [agent] cli was changed.")
			return nil
		},
	}

	remove := &cobra.Command{
		Use:   "remove <claude|grok|codex|all>",
		Short: "Remove satelle-owned launcher scripts (idempotent; unmarked files skipped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := config.GlobalDir()
			rs, err := agentinstall.Remove(home, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range rs {
				fmt.Fprintf(out, "%s %s → %s", r.Action, r.Name, r.Path)
				if r.Note != "" {
					fmt.Fprintf(out, " (%s)", r.Note)
				}
				fmt.Fprintln(out)
			}
			return nil
		},
	}

	agents.AddCommand(install, remove)
	register(agents)
}
