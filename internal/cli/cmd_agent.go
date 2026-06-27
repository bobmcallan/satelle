// `satelle agent` selects the headless agent CLI the quality-management spine
// shells out to for isolated reviews/summaries (the install-time choice, sty_b6973a7b).
// The selection persists in the global config (~/.satelle/config.toml [agent] cli)
// so every repo's reviewer resolves the same agent; the reviewer/summariser never
// name a binary directly.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	agent := &cobra.Command{
		Use:   "agent",
		Short: "Select the agent CLI the reviewer/summariser use (claude | codex)",
		Long: `agent manages which headless agent CLI satelle's quality-management spine
shells out to for isolated reviews and summaries. The choice persists in the
global config (~/.satelle/config.toml) so it is set once per machine.`,
	}

	show := &cobra.Command{
		Use:   "show",
		Short: "Show the selected agent CLI and whether it is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			cli := gc.Agent.ResolveCLI()
			source := "config"
			if gc.Agent.CLI == "" {
				source = "default"
			}
			onPath := "not found on PATH"
			if agentcli.Available(cli) {
				onPath = "on PATH"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "agent cli: %s (%s) — %s\n", cli, source, onPath)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <claude|codex>",
		Short: "Select and persist the agent CLI",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate the name against the known runners before persisting.
			if _, err := agentcli.NewRunner(args[0]); err != nil {
				return err
			}
			return persistAgentCLI(cmd, args[0])
		},
	}

	detect := &cobra.Command{
		Use:   "detect",
		Short: "Auto-detect an installed agent CLI (claude preferred) and persist it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			found := agentcli.Detect()
			if found == "" {
				return fmt.Errorf("no supported agent CLI found on PATH (looked for %q, %q) — install one, then `satelle agent set <cli>`",
					agentcli.CLIClaude, agentcli.CLICodex)
			}
			return persistAgentCLI(cmd, found)
		},
	}

	agent.AddCommand(show, set, detect)
	register(agent)
}

// persistAgentCLI saves cli into the global config, preserving other settings.
func persistAgentCLI(cmd *cobra.Command, cli string) error {
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	gc.Agent.CLI = cli
	if err := config.SaveGlobal(gc); err != nil {
		return err
	}
	note := ""
	if !agentcli.Available(cli) {
		note = " (warning: not currently on PATH)"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "agent cli set to %s%s → %s\n", cli, note, config.GlobalConfigPath())
	return nil
}
