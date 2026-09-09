package cli

// satelle sync bindings — agent bindings on the workspace sync path
// (sty_01949949, order:5 of epic:agent-messaging).
//
// push publishes the repo's agents layer (.satelle/workflows/agents.toml) into
// the bound TEAM workspace's publish catalog under kind "agents", REDACTED by
// config.RedactAgentsTransport (env values, absolute command paths, profile
// names never leave the machine). pull fetches that catalog entry, redacts it
// AGAIN on ingest (a catalog entry from an older, unredacted path must not land
// live bindings), and writes it as the workspace layer
// .satelle/workflows/agents.workspace.toml — a separate file the resolver
// merges UNDER the repo's own agents.toml (a repo field wins; a workspace field
// fills a blank; a workspace-only table applies). Executables and ${VAR}
// references resolve on THIS machine at wiring/dispatch time.
//
// An unbound repo (no hosted server or no team workspace) is a no-op that
// contacts nothing — by design, not an error.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

const bindingsPublishTitle = "agents layer (workspace bindings)"

func newSyncBindingsCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "bindings",
		Short: "Publish this repo's agent bindings to the team workspace, or apply the workspace layer under agents.toml",
		Long: `Move the agents layer between this repo and its bound team workspace.

push publishes .satelle/workflows/agents.toml into the team catalog (kind
"agents") with env values, absolute command paths and profile names redacted:
the workspace decides roles, interface, model, effort and skills; executables
and credentials stay on each machine.

pull applies the catalog entry as .satelle/workflows/agents.workspace.toml — a
LAYER UNDER the repo's own agents.toml. A repo field wins, a workspace field
fills a blank, a workspace-only table applies. 'satelle agent validate' names
the source of every effective field.

A repo with no hosted server or no team workspace does nothing and contacts
nothing.`,
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Publish the redacted agents layer to the team workspace catalog (kind agents)",
		Long: `push reads this repo's .satelle/workflows/agents.toml (the REPO layer only —
never the resolved layer, so the machine-wide profile catalog is not exported),
redacts it, and publishes it into the bound team workspace's catalog under kind
"agents" at workflows/agents.toml. Identical content is idempotent.

Redaction is what every agents-kind transport shares: literal env values are
blanked (keys and pure ${VAR} references survive), secret-shaped settings and
settings.env values are blanked the same way, absolute paths become their base
name, profile= is dropped. The body is re-loaded after redaction; one that does
not parse is an error, never sent.

--dry-run prints the redacted layer and contacts nothing. A repo with no hosted
server or no team workspace does nothing and contacts nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncBindingsPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Team workspace to publish into (overrides the active workspace).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "Print the redacted layer that would be published; contact nothing.")
	group.AddCommand(push)

	var pullServer, pullWorkspace string
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Apply the team workspace's agents layer under this repo's agents.toml",
		Long: `pull fetches the team workspace's published workflows/agents.toml, redacts it
again on ingest, and writes it to .satelle/workflows/agents.workspace.toml.
The authored agents.toml is never rewritten.

That file is a LAYER UNDER the repo's agents.toml: a repo field wins, a
workspace field fills a repo blank, a workspace-only table applies whole. A
blank the redaction left is dropped, never merged, so it cannot shadow this
machine's environment; executables resolve on the local PATH and ${VAR} from
the local [vars]. 'satelle agent validate' names each field's source; see
'satelle help agent-dispatch' for the full contract.

Nothing published → a note and no file. No hosted server or no team workspace
→ nothing done, nothing contacted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncBindingsPull(cmd, pullServer, pullWorkspace)
		},
	}
	pull.Flags().StringVar(&pullServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	pull.Flags().StringVar(&pullWorkspace, "workspace", "", "Team workspace to apply from (overrides the active workspace).")
	group.AddCommand(pull)

	return group
}

// bindingsTarget resolves server + team workspace for the bindings verbs.
// ok=false is the unbound no-op: nothing hosted is contacted (AC5).
func bindingsTarget(cfg config.Config, serverArg, workspaceArg string) (server, team string, ok bool) {
	server = resolveServer(serverArg)
	if server == "" {
		return "", "", false
	}
	team = resolveTeamWorkspaceName(cfg, workspaceArg)
	if team == "" {
		return "", "", false
	}
	return server, team, true
}

func runSyncBindingsPush(cmd *cobra.Command, serverArg, workspaceArg string, dryRun bool) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	server, team, ok := bindingsTarget(cfg, serverArg, workspaceArg)
	if !ok {
		fmt.Fprintln(out, "sync bindings: nothing to do — this repo is not bound to a team workspace (no hosted call).")
		return nil
	}
	// The REPO layer only, never the resolved layer: publishing the resolved
	// config would export the machine-wide catalog's profiles into the workspace.
	path, _ := config.AgentsPath(dataDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("sync bindings push: no %s in this repo", config.AgentsRel)
		}
		return fmt.Errorf("read %s: %w", config.AgentsRel, err)
	}
	body, err := config.RedactAgentsTransport(raw)
	if err != nil {
		return fmt.Errorf("sync bindings push: %w", err)
	}
	if dryRun {
		fmt.Fprintf(out, "Would publish %s (kind agents, redacted) to team %q on %s:\n\n%s", config.AgentsRel, team, server, body)
		return nil
	}
	// newHostedClient stamps x-satelle-location (sty_e88d77ce) — the only way
	// AC4 can regress is a bare hosted.NewClient here.
	client := newHostedClient(cmd.Context(), server, repoRoot)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), team)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	item, err := client.PublishFile(cmd.Context(), wsID, config.AgentsRel, "agents", bindingsPublishTitle, body)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) || errors.Is(err, hosted.ErrNotPublisher) {
			return err
		}
		return fmt.Errorf("publish %s: %w", config.AgentsRel, err)
	}
	if item.Created {
		fmt.Fprintf(out, "published %s v%d (new) to team %q — redacted: env values, absolute command paths, profile names\n", config.AgentsRel, item.Version, team)
	} else {
		fmt.Fprintf(out, "%s unchanged at v%d on team %q\n", config.AgentsRel, item.Version, team)
	}
	return nil
}

func runSyncBindingsPull(cmd *cobra.Command, serverArg, workspaceArg string) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	server, team, ok := bindingsTarget(cfg, serverArg, workspaceArg)
	if !ok {
		fmt.Fprintln(out, "sync bindings: nothing to do — this repo is not bound to a team workspace (no hosted call).")
		return nil
	}
	client := newHostedClient(cmd.Context(), server, repoRoot)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), team)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	content, meta, err := client.PublishedContent(cmd.Context(), wsID, config.AgentsRel, 0)
	if err != nil {
		if errors.Is(err, hosted.ErrPublishNotFound) {
			fmt.Fprintf(out, "sync bindings: team %q has published no agents layer — nothing applied.\n", team)
			return nil
		}
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("fetch published %s: %w", config.AgentsRel, err)
	}
	// Ingest does not trust the catalog: redact again so a pre-existing
	// unredacted entry cannot land absolute paths or env values as live
	// bindings. A body that does not decode is refused; nothing is written.
	body, err := config.RedactAgentsTransport(content)
	if err != nil {
		return fmt.Errorf("sync bindings pull: refusing to apply team %q agents layer v%d: %w", team, meta.Version, err)
	}
	dest := config.WorkspaceAgentsPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", config.WorkspaceAgentsRel, err)
	}
	fmt.Fprintf(out, "applied team %q agents layer v%d → %s (layer under %s; executables and ${VAR} resolve locally)\n",
		team, meta.Version, config.WorkspaceAgentsRel, config.AgentsRel)
	return nil
}
