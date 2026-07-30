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
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
on satelle workflow validate — this command reuses them alongside the agent layer.

To install satelle-owned launcher scripts (e.g. Codex ACP adapter wrapper), use
satelle agents install (plural) — that path never changes this [agent] cli default.`,
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

	agent.AddCommand(show, set, detect, agentValidateCmd(), agentProfilesCmd(), agentMigrateCmd())
	register(agent)
}

// agentProfilesCmd is `satelle agent profiles` — the machine-wide catalog view.
// Read-only, store-free, and repo-independent: it answers "what execution
// profiles does this machine offer?" without saying anything about which repo
// consumes them (that is each repo's explicit `profile =` reference).
func agentProfilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profiles",
		Short: "List the machine-wide agent profile catalog (~/.satelle/agents.toml)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			global, err := config.LoadGlobalAgents()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(global.Profiles) == 0 && len(global.Roles) == 0 {
				fmt.Fprintf(out, "no profiles defined — %s is absent or empty\n", config.GlobalAgentsPath())
				fmt.Fprintln(out, "run `satelle agent migrate` to seed a starter catalog from the selected agent CLI")
				return nil
			}
			printProfileCatalog(out, global)
			return nil
		},
	}
}

// agentMigrateCmd is `satelle agent migrate` — the opt-in, non-destructive path
// from the legacy machine-wide setting (`~/.satelle/config.toml [agent] cli`) to
// a profile catalog. It NEVER runs automatically and never overwrites: an
// existing catalog is left alone. Repo-only installations need no migration at
// all — with no catalog present every repo resolves exactly as before.
func agentMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Seed ~/.satelle/agents.toml with a starter profile from the selected agent CLI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			path := config.GlobalAgentsPath()
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(out, "%s already exists — edit it by hand; migrate never overwrites a catalog\n", path)
				return nil
			}
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			cli := gc.Agent.ResolveCLI()
			body, err := config.StarterGlobalAgents(cli)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(out, "wrote %s — one %q profile derived from [agent] cli\n", path, cli)
			fmt.Fprintln(out, "reference it from a repo with `profile = \"<name>\"` in .satelle/agents.toml; nothing changes until you do")
			return nil
		},
	}
}

// printProfileCatalog renders the machine-wide catalog. Secret containment: env
// and settings VALUES are never printed — only their key names, the same rule
// the grant display already follows.
func printProfileCatalog(out io.Writer, g config.GlobalAgentsConfig) {
	if len(g.Profiles) == 0 && len(g.Roles) == 0 {
		return
	}
	fmt.Fprintf(out, "Global agent profiles (%s — execution configuration; workflow policy stays repo substrate):\n",
		config.GlobalAgentsLabel)
	for _, name := range sortedBindingNames(g.Profiles) {
		b := g.Profiles[name]
		fmt.Fprintf(out, "  PROFILE [%s] role=%q interface=%s model=%q effort=%q tools=%q timeout=%q\n",
			name, b.Role, b.ResolvedInterface(), b.Model, b.Effort, b.Tools, b.Timeout)
		if b.Profile != "" {
			fmt.Fprintf(out, "          extends: %s\n", b.Profile)
		}
		if b.Command != "" {
			fmt.Fprintf(out, "          command: %s\n", b.Command)
		}
		if len(b.Env) > 0 {
			fmt.Fprintf(out, "          env keys: %s\n", strings.Join(sortedMapKeys(b.Env), ","))
		}
	}
	for _, role := range sortedMapKeys(g.Roles) {
		fmt.Fprintf(out, "  ROLE DEFAULT %s -> %s (applies only to a repo with [defaults] use_global_roles = true)\n",
			role, g.Roles[role])
	}
}

// printGrantSources renders each effective field with the tier that supplied it
// (AC4): repo inline, an explicitly referenced profile, an opt-in role default,
// or the binary's embedded fallback. env/settings values may be secrets, so
// those lines name the field and its source only.
func printGrantSources(out io.Writer, g agentvalidate.Grant) {
	if len(g.Sources) == 0 {
		return
	}
	vals := map[string]string{
		"interface":  g.Interface,
		"command":    g.Command,
		"tools":      g.Tools,
		"model":      g.Model,
		"effort":     g.Effort,
		"timeout":    g.Timeout,
		"role":       g.Role,
		"principles": g.Principles,
		"secondary":  g.Secondary,
	}
	for _, f := range sortedMapKeys(g.Sources) {
		if f == "env" || f == "settings" {
			fmt.Fprintf(out, "         source: %s (%s) — values withheld\n", f, g.Sources[f])
			continue
		}
		fmt.Fprintf(out, "         source: %s = %q (%s)\n", f, vals[f], g.Sources[f])
	}
}

func sortedBindingNames(m map[string]config.AgentBinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
			dataDir := a.DataDir
			if dataDir == "" {
				dataDir = a.Config.ResolveDataDir(a.RepoRoot)
			}

			agents, lerr := config.LoadAgents(dataDir)
			if lerr != nil {
				return fmt.Errorf("agents.toml: %w", lerr)
			}
			// The machine-wide profile catalog is EXECUTION configuration only
			// (sty_c7dfeedf). A broken catalog is reported here as a problem, not a
			// hard error, so one run shows every finding.
			global, gerr := config.LoadGlobalAgents()
			if gerr != nil {
				fmt.Fprintf(out, "FAIL  %v\n", gerr)
			}
			wfs, werr := a.Store.DocIndex.List(context.Background(), "workflows")
			if werr != nil {
				return werr
			}

			// Agent layer + node→binding (shared with init + engage), resolved
			// against the catalog so what is judged is what will actually run.
			report := agentvalidate.ValidateEffective(agents, global, a.Config.Vars, wfs)
			printProfileCatalog(out, global)
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
				fmt.Fprintf(out, "  GRANT [%s] role=%s principles=%s constitution=%s interface=%s backend=%s %s tools=%q model=%q effort=%q timeout=%q inject_principles=%v\n",
					g.Name, roleNote, g.Principles, consti, g.Interface, g.Backend, ro, g.Tools, g.Model, g.Effort, g.Timeout, g.InjectsPrinciples)
				if g.Notes != "" {
					fmt.Fprintf(out, "         notes: %s\n", g.Notes)
				}
				printGrantSources(out, g)
			}
			if len(report.Gates) > 0 {
				fmt.Fprintln(out, "Gate/node effective models (binding that will run the gate):")
				for _, ga := range report.Gates {
					fmt.Fprintf(out, "  NODE [%s] %s gate=%s agent=%s effective_model=%q\n",
						ga.Workflow, ga.Node, ga.Skill, ga.Agent, ga.EffectiveModel)
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
