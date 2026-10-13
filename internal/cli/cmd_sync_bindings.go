package cli

// satelle sync bindings — agent bindings on the workspace sync path
// (sty_01949949, order:5 of epic:agent-messaging).
//
// push publishes the repo's agents layer (.satelle/workflows/agents.toml) into
// the bound TEAM workspace's publish catalog under kind "agents", exactly as it
// is on disk — after config.CheckAgents has REFUSED a file that carries
// machine-specific content (env values, absolute command paths, profile names
// belong in agents.local.toml, which never leaves the machine) and the deployed
// system has validated. pull fetches that catalog entry, checks it AGAIN on
// ingest (a catalog entry from an older push must not land machine-specific
// values as live bindings), and writes it as the workspace layer
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
"agents") exactly as it is on disk, and refuses a file that carries
machine-specific content — env values, absolute command paths, profile names:
the workspace decides roles, interface, model, effort and skills; executables
and credentials stay on each machine, in .satelle/workflows/agents.local.toml.

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
		Short: "Publish the agents layer to the team workspace catalog (kind agents)",
		Long: `push reads this repo's .satelle/workflows/agents.toml (the REPO layer only —
never the resolved layer, so the machine-wide profile catalog is not exported)
and publishes it, byte for byte, into the bound team workspace's catalog (kind
"agents"). Identical content is idempotent.

Before any network call it refuses machine-specific content — a literal env
value, an absolute path, profile=, use_global_roles — naming the field and
pointing at .satelle/workflows/agents.local.toml, which never leaves this
machine. See 'satelle help agent-dispatch'.

--dry-run runs those checks and contacts nothing. A repo with no hosted server
or no team workspace does nothing and contacts nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncBindingsPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Team workspace to publish into (overrides the active workspace).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "Run the refusal and validation checks and print the layer that would be published; contact nothing.")
	group.AddCommand(push)

	var pullServer, pullWorkspace string
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Apply the team workspace's agents layer under this repo's agents.toml",
		Long: `pull fetches the team workspace's published workflows/agents.toml, checks it
on ingest, and writes it as published to .satelle/workflows/agents.workspace.toml.
agents.toml and agents.local.toml are never rewritten. A published layer that
carries machine-specific content is refused, naming the field.

That file is a LAYER UNDER the repo's agents.toml: a repo field wins, a
workspace field fills a repo blank, a workspace-only table applies whole. A
blank value is dropped, never merged; executables resolve on the local PATH and
${VAR} from the local [vars]. 'satelle agent validate' names each field's
source; see 'satelle help agent-dispatch' for the full contract.

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
	// Refuse before any network call — a dry run included. The body that is sent
	// is the file's own bytes: nothing is rewritten on the way out (sty_89b9df20).
	if err := checkPushFiles("sync bindings push", []config.ConfigFile{{Area: "agents", Path: config.AgentsRel, Content: raw}}); err != nil {
		return err
	}
	if err := preflightPush(out, dataDir); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, "Would publish %s (kind agents) to team %q on %s:\n\n%s", config.AgentsRel, team, server, raw)
		return nil
	}
	// newHostedClient stamps x-satelle-location (sty_e88d77ce) — the only way
	// AC4 can regress is a bare hosted.NewClient here.
	client := newHostedClient(cmd.Context(), server, repoRoot)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), team)
	if err != nil {
		return fmt.Errorf("resolve team workspace: %w", err)
	}
	item, err := client.PublishFile(cmd.Context(), wsID, config.AgentsRel, "agents", bindingsPublishTitle, raw)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) || errors.Is(err, hosted.ErrNotPublisher) {
			return err
		}
		return fmt.Errorf("publish %s: %w", config.AgentsRel, err)
	}
	if item.Created {
		fmt.Fprintf(out, "published %s v%d (new) to team %q\n", config.AgentsRel, item.Version, team)
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
	// Ingest does not trust the catalog: check again so a pre-existing entry that
	// carries absolute paths or env values cannot land them as live bindings. A
	// body that does not decode, or carries machine-specific content, is refused
	// with the pointer to agents.local.toml; nothing is written or altered.
	if err := ingestAgents(fmt.Sprintf("sync bindings pull (team %q v%d)", team, meta.Version), content); err != nil {
		return err
	}
	dest := config.WorkspaceAgentsPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", config.WorkspaceAgentsRel, err)
	}
	fmt.Fprintf(out, "applied team %q agents layer v%d → %s (layer under %s; executables and ${VAR} resolve locally)\n",
		team, meta.Version, config.WorkspaceAgentsRel, config.AgentsRel)
	return nil
}
