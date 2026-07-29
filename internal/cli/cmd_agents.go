// `satelle agents install|remove` provisions satelle-owned launchers under
// $SATELLE_HOME/agents/bin/ and repo harness compliance scaffolds
// (.claude / .grok / .codex blocking hooks) (sty_aa726901, sty_9e86f407).
// Distinct from `satelle agent` (singular: select/validate the headless CLI).
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentinstall"
	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	agents := &cobra.Command{
		Use:   "agents",
		Short: "Install or remove satelle-owned agent launchers and harness compliance hooks (claude | grok | codex | all)",
		Long: `agents install and remove manage two satelle-owned surfaces per target:

  1. Launcher scripts under $SATELLE_HOME/agents/bin/ (e.g. satelle-codex →
     npx -y @agentclientprotocol/codex-acp — no unsupported stdio subcommand).
  2. Repo harness compliance scaffolds with blocking PreToolUse hooks:
     .claude/settings.json, .grok/hooks/satelle.json, .codex/hooks.json —
     so governed code-changing actions are denied unless a satelle story is
     engaged (same policy as satelle hook gate / commitgate).

Compliance guarantee: Claude, Grok, and Codex hook paths deny governed
mutations when no story is engaged, and apply the normal engaged-story policy
when one is.

Ownership boundary: only satelle-owned artifacts (marker-bearing launchers and
hook entries whose command references satelle-hook.sh / satelle hook) are
created, updated, or removed. User-authored harness keys and non-satelle hooks
are preserved. Install and remove are idempotent.

They do not install third-party packages globally, do not change
~/.satelle/config.toml [agent] cli, and do not edit any repo's agents.toml —
so a Codex install path can be verified without changing the default reviewer.

Codex hooks require trust on first use (Codex /hooks review). Automation may
pass --dangerously-bypass-hook-trust to codex exec.

For selecting or validating the headless agent CLI, use satelle agent (singular):
  satelle agent show | set | detect | validate`,
	}

	install := &cobra.Command{
		Use:   "install <claude|grok|codex|all>",
		Short: "Install launchers + harness compliance hooks (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := config.GlobalDir()
			repoRoot := initRepoRoot("")
			rs, err := agentinstall.Install(home, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range rs {
				fmt.Fprintf(out, "%s %s launcher → %s", r.Action, r.Name, r.Path)
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
			// Harness compliance scaffolds (repo-local).
			targets, err := expandAgentTargets(args[0])
			if err != nil {
				return err
			}
			if err := writeHookScripts(repoRoot); err != nil {
				return err
			}
			for _, name := range targets {
				switch name {
				case "claude":
					added, updated, incomplete, err := ensureClaudeHooks(repoRoot)
					if err != nil {
						return err
					}
					printScaffoldOutcome(out, "claude", ".claude/settings.json", added, updated, incomplete)
				case "grok":
					added, updated, incomplete, err := ensureGrokHooks(repoRoot)
					if err != nil {
						return err
					}
					printScaffoldOutcome(out, "grok", grokHooksRel, added, updated, incomplete)
				case "codex":
					added, updated, incomplete, err := ensureCodexHooks(repoRoot)
					if err != nil {
						return err
					}
					printScaffoldOutcome(out, "codex", codexHooksRel, added, updated, incomplete)
					fmt.Fprintln(out, "  note: Codex will prompt to trust .codex/hooks.json on first run (/hooks); automation may use --dangerously-bypass-hook-trust")
				}
			}
			fmt.Fprintln(out, "No default reviewer or [agent] cli was changed.")
			fmt.Fprintln(out, "Compliance: governed code edits/commits require an engaged satelle story (hook gate/commitgate).")
			return nil
		},
	}

	remove := &cobra.Command{
		Use:   "remove <claude|grok|codex|all>",
		Short: "Remove satelle-owned launchers and hook scaffolds (idempotent; unmarked left in place)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := config.GlobalDir()
			repoRoot := initRepoRoot("")
			rs, err := agentinstall.Remove(home, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range rs {
				fmt.Fprintf(out, "%s %s launcher → %s", r.Action, r.Name, r.Path)
				if r.Note != "" {
					fmt.Fprintf(out, " (%s)", r.Note)
				}
				fmt.Fprintln(out)
			}
			targets, err := expandAgentTargets(args[0])
			if err != nil {
				return err
			}
			for _, name := range targets {
				var action, path, note string
				var rerr error
				switch name {
				case "claude":
					action, path, note, rerr = removeClaudeHooks(repoRoot)
				case "grok":
					action, path, note, rerr = removeGrokHooks(repoRoot)
				case "codex":
					action, path, note, rerr = removeCodexHooks(repoRoot)
				}
				if rerr != nil {
					return rerr
				}
				fmt.Fprintf(out, "%s %s scaffold → %s", action, name, path)
				if note != "" {
					fmt.Fprintf(out, " (%s)", note)
				}
				fmt.Fprintln(out)
			}
			// Shared wrapper: remove only when no remaining scaffold references it.
			if action, path, note, err := maybeRemoveSharedHookScript(repoRoot); err != nil {
				return err
			} else if action != "" {
				fmt.Fprintf(out, "%s shared-hook → %s", action, path)
				if note != "" {
					fmt.Fprintf(out, " (%s)", note)
				}
				fmt.Fprintln(out)
			}
			return nil
		},
	}

	agents.AddCommand(install, remove)
	register(agents)
}

func expandAgentTargets(name string) ([]string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "all":
		return []string{"claude", "grok", "codex"}, nil
	case "claude", "grok", "codex":
		return []string{n}, nil
	default:
		return nil, fmt.Errorf("agents: unknown agent %q (want claude, grok, codex, or all)", name)
	}
}

func printScaffoldOutcome(out io.Writer, name, rel string, added bool, updated, incomplete []string) {
	if len(updated) > 0 {
		fmt.Fprintf(out, "updated %s scaffold → %s (%s)\n", name, rel, strings.Join(updated, "; "))
	} else if added {
		fmt.Fprintf(out, "created %s scaffold → %s\n", name, rel)
	} else {
		fmt.Fprintf(out, "unchanged %s scaffold → %s\n", name, rel)
	}
	if len(incomplete) > 0 {
		fmt.Fprintf(out, "WARN  %s — incomplete satelle hooks after heal: missing %s\n",
			rel, strings.Join(incomplete, ", "))
	}
}
