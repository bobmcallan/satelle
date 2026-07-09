package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
)

// `satelle sync` is the scoped-sync command group (epic:scoped-sync): scopes
// inspection, config push/deploy, and documents push/pull. v1 has per-kind
// commands only — no single unified `satelle sync` that routes every area.
func init() {
	syncCmd := &cobra.Command{Use: "sync", Short: "Scope inspection and per-kind sync (config, documents, workstate)"}
	syncCmd.AddCommand(&cobra.Command{
		Use:         "scopes",
		Short:       "Print each .satelle area's resolved scope, and shared files within a personal area",
		Annotations: needsStore(),
		RunE:        runSyncScopes,
	})
	syncCmd.AddCommand(newSyncConfigCmd())
	syncCmd.AddCommand(newSyncDocumentsCmd())
	syncCmd.AddCommand(newSyncWorkstateCmd())
	register(syncCmd)
}

// newSyncConfigCmd builds the `satelle sync config` group — the CLI counterpart
// to the hosted server's versioned, per-user config store (epic:scoped-sync,
// order:5). push uploads authored config per its RESOLVED scope (skipping local,
// honoring the per-file shared flag) as new per-file versions; deploy/pull
// materializes the latest (or a pinned) version into this repo byte-for-byte —
// "set up project X like project Y". These commands are not store-backed: they
// load the repo config directly and talk to the hosted server, like login/project.
func newSyncConfigCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "config",
		Short: "Push authored config to / deploy it from the hosted server's versioned config store",
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload authored config to the versioned store (a new version per file)",
		Long: `push walks the authored-config areas (workflows/principles/skills/constitution/
agents/tasks) per their resolved [sync] scope — skipping local areas and honoring the
per-file shared flag — and uploads each file as a new version into the destination
workspace on the hosted server. Identical content is idempotent (no new version).
Shared-tier files route to the team workspace; the rest to your personal workspace.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncConfigPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Team-workspace name shared-tier files route to (overrides the active workspace).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be pushed and the destination tier, without contacting the server.")
	group.AddCommand(push)

	var deployServer, deployWorkspace string
	var version int
	deploy := &cobra.Command{
		Use:     "deploy",
		Aliases: []string{"pull"},
		Short:   "Materialize config from the store into this repo (set up X like Y)",
		Long: `deploy fetches the active (or --workspace) workspace's config manifest and writes
every file byte-for-byte into this repo's data dir. --version N pins a per-file version
(default: latest). This is the "set up project X like project Y" operation: point a fresh
repo at a team workspace and deploy to inherit its authored config.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncConfigDeploy(cmd, deployServer, deployWorkspace, version)
		},
	}
	deploy.Flags().StringVar(&deployServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	deploy.Flags().StringVar(&deployWorkspace, "workspace", "", "Workspace to deploy from (overrides the active workspace).")
	deploy.Flags().IntVar(&version, "version", 0, "Pin a per-file version (default: latest head of each path).")
	group.AddCommand(deploy)

	return group
}

// loadRepoConfig loads the repo config and the resolved repo root + data dir for
// a (non-store-backed) sync config command. A missing config is not an error —
// the zero config runs on defaults (all-local -> nothing to push).
func loadRepoConfig() (config.Config, string, string, error) {
	cfg, cfgPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return config.Config{}, "", "", fmt.Errorf("load config: %w", err)
	}
	repoRoot := "."
	if cfgPath != "" {
		repoRoot = config.RepoRootFromConfigPath(cfgPath)
	} else if cwd, e := os.Getwd(); e == nil {
		repoRoot = cwd
	}
	return cfg, repoRoot, cfg.ResolveDataDir(repoRoot), nil
}

// resolveTeamWorkspaceName is the shared-tier destination for a push: the
// --workspace flag wins, else the configured active workspace when it is a team
// workspace, else "" (no team destination — shared files then error out).
func resolveTeamWorkspaceName(cfg config.Config, workspaceArg string) string {
	if ws := strings.TrimSpace(workspaceArg); ws != "" {
		return ws
	}
	active := cfg.ResolveActiveWorkspace()
	if active.IsPersonal() {
		return ""
	}
	return active.Name
}

// resolveDeploySourceName is the workspace a deploy reads from: the --workspace
// flag wins, else the configured active workspace (personal default is valid —
// deploy your own personal config).
func resolveDeploySourceName(cfg config.Config, workspaceArg string) string {
	if ws := strings.TrimSpace(workspaceArg); ws != "" {
		return ws
	}
	return cfg.ResolveActiveWorkspace().Name
}

func runSyncConfigPush(cmd *cobra.Command, serverArg, workspaceArg string, dryRun bool) error {
	cfg, repoRoot, _, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	files, err := config.ConfigFiles(cfg, repoRoot)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(files) == 0 {
		fmt.Fprintln(out, "No config to push — every config area is local. Set [sync] <area> = personal|shared to opt in.")
		return nil
	}
	teamName := resolveTeamWorkspaceName(cfg, workspaceArg)
	if dryRun {
		fmt.Fprintf(out, "Would push %d config file(s) to %s:\n", len(files), server)
		for _, f := range files {
			dest := "your personal workspace"
			if f.Tier == config.SharedTier {
				if teamName == "" {
					dest = "(no team workspace selected)"
				} else {
					dest = fmt.Sprintf("team workspace %q", teamName)
				}
			}
			fmt.Fprintf(out, "  [%s] %-40s -> %s\n", f.Tier, f.Path, dest)
		}
		return nil
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	personalID, err := client.ActiveWorkspaceID(cmd.Context(), config.PersonalWorkspace)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	var teamID string
	if teamName != "" {
		if teamID, err = client.ActiveWorkspaceID(cmd.Context(), teamName); err != nil {
			return fmt.Errorf("resolve team workspace: %w", err)
		}
	}
	var created, skipped int
	for _, f := range files {
		wsID := personalID
		if f.Tier == config.SharedTier {
			if teamID == "" {
				return fmt.Errorf("shared config %q has no team workspace — run \"satelle login --workspace <team>\" or pass --workspace to select one", f.Path)
			}
			wsID = teamID
		}
		res, perr := client.PushConfigFile(cmd.Context(), wsID, f.Path, f.Content)
		if perr != nil {
			if errors.Is(perr, hosted.ErrLoginRequired) {
				return perr
			}
			return fmt.Errorf("push %s: %w", f.Path, perr)
		}
		if res.Created {
			created++
		} else {
			skipped++
		}
	}
	fmt.Fprintf(out, "Pushed %d config file(s) to %s: %d new, %d unchanged.\n", len(files), server, created, skipped)
	return nil
}

func runSyncConfigDeploy(cmd *cobra.Command, serverArg, workspaceArg string, version int) error {
	cfg, _, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	sourceName := resolveDeploySourceName(cfg, workspaceArg)
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), sourceName)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	manifest, err := client.ConfigManifest(cmd.Context(), wsID)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("fetch manifest: %w", err)
	}
	if len(manifest) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No config in workspace %q on %s.\n", sourceName, server)
		return nil
	}
	var files []subsync.File
	var missing int
	for _, item := range manifest {
		content, _, ferr := client.ConfigFileContent(cmd.Context(), wsID, item.Path, version)
		if ferr != nil {
			if errors.Is(ferr, hosted.ErrConfigFileMissing) && version > 0 {
				missing++ // a file this version predates — skip it
				continue
			}
			if errors.Is(ferr, hosted.ErrLoginRequired) {
				return ferr
			}
			return fmt.Errorf("fetch %s: %w", item.Path, ferr)
		}
		files = append(files, subsync.File{Path: item.Path, Content: content})
	}
	if len(files) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Nothing to deploy — version %d matched no files in workspace %q.\n", version, sourceName)
		return nil
	}
	n, err := subsync.Restore(dataDir, files)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	pinned := "latest"
	if version > 0 {
		pinned = fmt.Sprintf("version %d", version)
	}
	tail := ""
	if missing > 0 {
		tail = fmt.Sprintf(", %d skipped (not present at that version)", missing)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deployed %d file(s) (%s%s) from %s workspace %q into %s.\n", n, pinned, tail, server, sourceName, dataDir)
	return nil
}

func runSyncScopes(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, area := range config.SyncAreas {
		scope, err := config.ScopeFor(a.Config, area)
		if err != nil {
			return fmt.Errorf("sync area %q: %w", area, err)
		}
		fmt.Fprintf(w, "%s\t%s\n", area, scope)
		if scope != config.PersonalScope {
			continue
		}
		path, isDir := syncAreaPath(a, area)
		if path == "" {
			continue
		}
		shared, err := sharedFilesIn(path, isDir)
		if err != nil {
			return fmt.Errorf("sync area %q: %w", area, err)
		}
		for _, f := range shared {
			fmt.Fprintf(w, "\tshared: %s\n", f)
		}
	}
	return w.Flush()
}

// syncAreaPath resolves the on-disk location a sync area's files live under.
// isDir reports whether path is a directory to scan (vs. a single file).
// "ledger" is DB-only and "executions" nest under their parent task's
// directory — neither has an area-level location of its own, so both resolve
// to "" (no file scan, scope still printed).
func syncAreaPath(a *app.App, area string) (path string, isDir bool) {
	dataDir := filepath.Dir(a.DBPath)
	switch area {
	case "constitution":
		return a.Config.ResolveConstitution(a.RepoRoot), false
	case "agents":
		return filepath.Join(dataDir, config.AgentsConfigName), false
	case "tasks":
		return filepath.Join(dataDir, "tasks"), true
	case "stories":
		return filepath.Join(dataDir, "stories"), true
	case "ledger", "executions":
		return "", false
	default:
		if dir := a.Config.ResolveAuthoredDirs(a.RepoRoot)[area]; dir != "" {
			return dir, true
		}
		return "", false
	}
}

// sharedFilesIn returns the repo-relative paths of every markdown file under
// path (or path itself, if it is a single file) whose frontmatter marks it
// shared. A non-existent path is benign (empty result, not an error) — an
// area with nothing on disk yet has nothing to report.
func sharedFilesIn(path string, isDir bool) ([]string, error) {
	if !isDir {
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if config.FileShared(config.PersonalScope, string(body)) {
			return []string{path}, nil
		}
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == path {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		if name == "index" || name == "log" || strings.EqualFold(name, "README") {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if config.FileShared(config.PersonalScope, string(body)) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
