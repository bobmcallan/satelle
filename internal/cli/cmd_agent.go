// `satelle agent` selects the headless agent CLI the quality-management spine
// shells out to for isolated reviews/summaries (the install-time choice, sty_b6973a7b).
// The selection persists in the global config (~/.satelle/config.toml [agent] cli)
// so every repo's reviewer resolves the same agent; the reviewer/summariser never
// name a binary directly.
//
// `satelle agent validate` (sty_93eec36d) is the store-backed check of this repo's
// .satelle/agents.toml + workflow agent= bindings — the same authority init and
// story engagement use. It is deliberate that there is no top-level
// `satelle validate`: each noun validates its own.
package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
)

func init() {
	agent := &cobra.Command{
		Use:   "agent",
		Short: "Select the agent CLI the reviewer/summariser use (claude | codex); validate agents.toml",
		Long: `agent manages which headless agent CLI satelle's quality-management spine
shells out to for isolated reviews and summaries. The choice persists in the
global config (~/.satelle/config.toml) so it is set once per machine.

agent validate checks this repo's .satelle/agents.toml (every binding's command,
timeout, env) and each workflow's agent= node bindings, and surfaces each agent's
resolved grant. Structural workflow checks (rubrics, unresolved gate skills) stay
on satelle workflow validate — this command reuses them alongside the agent layer.`,
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

	agent.AddCommand(show, set, detect, agentValidateCmd())
	register(agent)
}

// agentValidateCmd is `satelle agent validate` — the standalone, deterministic
// check of agents.toml + workflow agent= bindings (sty_93eec36d). It also runs
// the structural workflow check and WorkflowConsistency so one call covers the
// mechanical agent↔workflow surface without duplicating those checks.
func agentValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "validate",
		Short:       "Validate .satelle/agents.toml and workflow agent= bindings; show resolved grants",
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			dataDir := filepath.Dir(a.DBPath)

			agents, lerr := config.LoadAgents(dataDir)
			if lerr != nil {
				return fmt.Errorf("agents.toml: %w", lerr)
			}
			wfs, werr := a.Store.DocIndex.List(context.Background(), "workflows")
			if werr != nil {
				return werr
			}

			// Agent layer + node→binding (shared with init + engage).
			report := agentvalidate.Validate(agents, a.Config.Vars, wfs)
			fmt.Fprintln(out, "Agent grants (resolved):")
			for _, g := range report.Grants {
				ro := "read-write"
				if g.ReadOnly {
					ro = "read-only"
				}
				consti := "no"
				if g.InjectsPrinciples {
					consti = "yes" // constitution rides order-zero when principles ≠ none
				}
				if g.Backend == "in-loop" {
					consti = "n/a (in-loop; session injects)"
				}
				roleNote := g.Role
				if g.RoleInferred {
					roleNote += " (inferred)"
				}
				fmt.Fprintf(out, "  GRANT [%s] role=%s principles=%s constitution=%s backend=%s %s tools=%q model=%q timeout=%q inject_principles=%v\n",
					g.Name, roleNote, g.Principles, consti, g.Backend, ro, g.Tools, g.Model, g.Timeout, g.InjectsPrinciples)
				if g.Notes != "" {
					fmt.Fprintf(out, "         notes: %s\n", g.Notes)
				}
			}
			failed := 0
			for _, w := range report.Warnings {
				fmt.Fprintf(out, "WARN  %s\n", w)
			}
			for _, p := range report.Problems {
				failed++
				fmt.Fprintf(out, "FAIL  %s\n", p)
			}
			if report.OK() {
				fmt.Fprintln(out, "PASS  agents.toml + workflow agent= bindings")
			}

			// Structural workflow checks + consistency (owned elsewhere; re-run so
			// one verb covers the mechanical agent↔workflow surface).
			resolve := skillResolver(a)
			_, f, _ := validateAuthoredDir(out, "workflows", filepath.Join(dataDir, "workflows"), "", resolve)
			failed += f
			for _, p := range agentstep.WorkflowConsistency(wfs, resolve) {
				failed++
				fmt.Fprintf(out, "FAIL  workflows (consistency) — %s\n", p)
			}

			if failed > 0 {
				return fmt.Errorf("agent validate: %d problem(s)", failed)
			}
			fmt.Fprintln(out, "PASS  agent validate green")
			return nil
		},
	}
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
