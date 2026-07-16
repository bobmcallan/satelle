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
// inspection, config push/deploy, and documents push/pull. Bare `satelle sync`
// is the default execute verb — it runs the configured sync across every
// opted-in area (config push, documents push+pull, work-state push) by composing
// the per-kind handlers; each self-gates on its [sync] scope. The per-kind
// subcommands remain for driving one area (or a one-way direction) on its own.
func init() {
	var syncServer string
	var syncDryRun bool
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Run the configured sync for every opted-in area (config, documents, workstate)",
		Long: `sync pushes/pulls the .satelle areas whose [sync] scope opts in to the hosted
server. Run bare, it executes the whole configured sync: authored config push,
documents push then pull, and work-state push — skipping any area still local.
The per-kind subcommands (config, documents, workstate) drive one area on its own.

Continuity posture: local areas stay on disk only. For personal areas, push is a
BACKUP to the bound hosted project; documents pull, config deploy, and workstate
pull are SYNC DOWN / rehydrate. .satelle content does not need to be git-tracked
for either. Bare sync does not run config deploy or workstate pull — use
"satelle sync config deploy", "satelle sync workstate pull", or "satelle sync
rehydrate" deliberately for recover.

Every area defaults to local — nothing leaves the machine until you set
[sync] <area> = personal. To opt the whole .satelle tree in with one key,
set [sync] all = personal (a per-area <area> = ... still overrides it).
Personal opt-in targets this repo's bound hosted project only (never a shared
dump across all personal projects under your account). Bind with "satelle
project bind <slug>" (or set [hosted] project in .satelle/satelle.toml). Team
catalogs are a separate verb: satelle publish.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, syncServer, syncDryRun)
		},
	}
	syncCmd.Flags().StringVar(&syncServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Preview what each opted-in area would push without contacting the server (documents pull is not previewed).")
	syncCmd.AddCommand(&cobra.Command{
		Use:         "scopes",
		Short:       "Print each .satelle area's resolved scope, and shared files within a personal area",
		Annotations: needsStore(),
		RunE:        runSyncScopes,
	})
	syncCmd.AddCommand(newSyncConfigCmd())
	syncCmd.AddCommand(newSyncDocumentsCmd())
	syncCmd.AddCommand(newSyncWorkstateCmd())
	syncCmd.AddCommand(newSyncRehydrateCmd())
	register(syncCmd)
}

// newSyncRehydrateCmd is the recover path (epic:workspace-rehydrate order:4):
// config deploy → documents pull → workstate pull. Does not push.
// Not store-annotated: config deploy must run before agents.toml exists on an
// empty tree; the store is opened after deploy for workstate pull only.
func newSyncRehydrateCmd() *cobra.Command {
	var server string
	var force bool
	cmd := &cobra.Command{
		Use:     "rehydrate",
		Aliases: []string{"pull"},
		Short:   "Sync-down recover: config deploy + documents pull + workstate pull (no push)",
		Long: `rehydrate is the empty-tree / wiped-.satelle recover path for personal opt-in.

Full operator path (pinned so scopes return before scope-gated pulls):
  1. install satelle          — binary on PATH
  2. satelle login            — global credentials
  3. satelle project bind <slug>
       creates .satelle/satelle.toml with [hosted] project when absent
  4. satelle sync rehydrate   — this command, which runs:
       a. config deploy       — restores satelle.toml / substrate including [sync]
       b. documents pull
       c. workstate pull      — stories/executions/ledger into the local store

Scopes live in satelle.toml: without config deploy first, personal areas stay
local and pull no-ops. If every area is still local after deploy, rehydrate
prints an explicit note that scopes were not restored from hosted.

Does not push. Bare "satelle sync" remains the backup-oriented path on a healthy
machine. Optional --force is passed to workstate pull only (conflict override).`,
		RunE: func(c *cobra.Command, args []string) error {
			return runSyncRehydrate(c, server, force)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	cmd.Flags().BoolVar(&force, "force", false, "Pass --force to workstate pull (upsert on conflict).")
	return cmd
}

func runSyncRehydrate(cmd *cobra.Command, serverArg string, force bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "rehydrate: config deploy…")
	if err := runSyncConfigDeploy(cmd, serverArg, "", 0); err != nil {
		return err
	}
	// After deploy, if every sync area is still local, scopes were not restored
	// from hosted (empty deploy, or never pushed with personal opt-in).
	if note := rehydrateAllLocalNote(); note != "" {
		fmt.Fprintln(out, note)
	}
	fmt.Fprintln(out, "rehydrate: documents pull…")
	if err := runSyncDocumentsPull(cmd, serverArg, ""); err != nil {
		return err
	}
	// Open store after config deploy so agents.toml (and scopes) may exist.
	if err := openAppForCmd(cmd); err != nil {
		return fmt.Errorf("rehydrate: open store after config deploy: %w — ensure personal config was pushed (or run satelle init), then retry", err)
	}
	defer closeAppForCmd(cmd)
	fmt.Fprintln(out, "rehydrate: workstate pull…")
	if err := runSyncWorkstatePull(cmd, serverArg, false, force); err != nil {
		return err
	}
	fmt.Fprintln(out, "rehydrate: done.")
	return nil
}

// rehydrateAllLocalNote returns an operator-facing explanation when every
// [sync] area is still local after config deploy (scopes never came back from
// hosted). Empty string when at least one area is personal|shared.
func rehydrateAllLocalNote() string {
	cfg, _, _, err := loadRepoConfig()
	if err != nil {
		return ""
	}
	for _, area := range config.SyncAreas {
		scope, serr := config.ScopeFor(cfg, area)
		if serr != nil {
			return ""
		}
		if scope != config.LocalScope {
			return ""
		}
	}
	return "rehydrate: note — every .satelle area is still local after config deploy (no personal [sync] scopes restored from hosted). Documents and workstate pulls will no-op until you set [sync] <area> = personal on a machine that has content and push, or deploy config that already opts in."
}

// runSync is the bare `satelle sync` default action: run the configured sync
// across every opted-in area by composing the per-kind handlers, in a fixed
// order — authored config up, documents both ways, work-state up. Each handler
// self-gates on its own [sync] scope and prints its own result; a local area is
// a no-op. `sync config deploy` is intentionally NOT part of the bundle — it
// materialises a whole partition over local files ("set up X like Y"), which is
// a deliberate operation, not a routine sync. Any handler error stops the run
// (fail-fast) so a partial sync surfaces rather than hides.
func runSync(cmd *cobra.Command, serverArg string, dryRun bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	// Pre-flight: with nothing opted in, emit one clean line instead of four
	// per-verb skip messages (and touch no network). An explicitly invalid scope
	// still fails here, matching `sync scopes`.
	optedIn := false
	for _, area := range config.SyncAreas {
		scope, serr := config.ScopeFor(a.Config, area)
		if serr != nil {
			return fmt.Errorf("sync area %q: %w", area, serr)
		}
		if scope != config.LocalScope {
			optedIn = true
		}
	}
	out := cmd.OutOrStdout()
	if !optedIn {
		fmt.Fprintln(out, "Nothing to sync — every .satelle area is local. Set [sync] <area> = personal (or [sync] all = personal) to opt in.")
		return nil
	}
	if err := runSyncConfigPush(cmd, serverArg, "", dryRun); err != nil {
		return err
	}
	if err := runSyncDocumentsPush(cmd, serverArg, "", dryRun); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintln(out, "documents pull: skipped under --dry-run (pull has no preview).")
	} else if err := runSyncDocumentsPull(cmd, serverArg, ""); err != nil {
		return err
	}
	return runSyncWorkstatePush(cmd, serverArg, dryRun)
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
		Short: "Push/deploy authored config for this repo's bound hosted project (local default: no hosted write)",
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload authored config to the versioned store (a new version per file)",
		Long: `push walks the authored-config areas (workflows/principles/skills/constitution/
agents/tasks) per their resolved [sync] scope — skipping local areas — and uploads
each file as a new version into this repo's bound hosted PROJECT's personal
collection only (epic:sync-publish). Identical content is idempotent (no new
version). Team is not a sync destination; use satelle publish to expose artifacts
to a team catalog. Requires "satelle project bind <slug>".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncConfigPush(cmd, pushServer, pushWorkspace, dryRun)
		},
	}
	push.Flags().StringVar(&pushServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	push.Flags().StringVar(&pushWorkspace, "workspace", "", "Ignored for push (sync is personal-only; kept for flag compatibility).")
	push.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be pushed without contacting the server.")
	group.AddCommand(push)

	var deployServer, deployWorkspace string
	var version int
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Materialize config from the store into this repo (set up X like Y)",
		Long: `deploy fetches this repo's bound hosted PROJECT's personal config collection
(or an explicit --workspace when reading another developer's personal set) and
writes every file byte-for-byte into this repo's data dir. --version N pins a
per-file version (default: latest). This is the "set up project X like project Y"
operation. Requires "satelle project bind <slug>". Local [sync] areas still
default to writing nothing hosted on push; deploy always materializes from the
bound project partition.`,
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

// sharedSyncNote warns when files would have used the old shared-tier team
// destination. Sync is personal-only (epic:sync-publish); publish is the team path.
func sharedSyncNote(files []config.ConfigFile) string {
	for _, f := range files {
		if f.Tier == config.SharedTier {
			return "note: [sync] shared / shared:true no longer routes sync to a team workspace — pushing to personal only. Use satelle publish to expose artifacts to a team catalog."
		}
	}
	return ""
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
	_ = workspaceArg // sync is personal-only (epic:sync-publish)
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
		fmt.Fprintln(out, "No config to push — every config area is local. Set [sync] <area> = personal to opt in.")
		return nil
	}
	if note := sharedSyncNote(files); note != "" {
		fmt.Fprintln(out, note)
	}
	if dryRun {
		fmt.Fprintf(out, "Would push %d config file(s) to bound project personal collection on %s:\n", len(files), server)
		for _, f := range files {
			fmt.Fprintf(out, "  %-40s -> personal\n", f.Path)
		}
		return nil
	}
	// Bound project before any network (AC5).
	project, err := resolveBoundProject(cfg)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	personalID, err := client.ActiveWorkspaceID(cmd.Context(), config.PersonalWorkspace)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	var created, skipped int
	for _, f := range files {
		res, perr := client.PushConfigFile(cmd.Context(), personalID, project, f.Path, f.Content)
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
	fmt.Fprintf(out, "Pushed %d config file(s) to project %q personal collection on %s: %d new, %d unchanged.\n", len(files), project, server, created, skipped)
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
	// Config store routes require ?project= (server sty_0e56fe79). Personal
	// deploy uses the bound project; explicit --workspace still needs a project
	// binding to select which partition to read.
	project, err := resolveBoundProject(cfg)
	if err != nil {
		return err
	}
	sourceName := resolveDeploySourceName(cfg, workspaceArg)
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	wsID, err := client.ActiveWorkspaceID(cmd.Context(), sourceName)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	manifest, err := client.ConfigManifest(cmd.Context(), wsID, project)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("fetch manifest: %w", err)
	}
	if len(manifest) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No config in workspace %q project %q on %s.\n", sourceName, project, server)
		return nil
	}
	var files []subsync.File
	var missing int
	for _, item := range manifest {
		content, _, ferr := client.ConfigFileContent(cmd.Context(), wsID, project, item.Path, version)
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
	res, err := subsync.Restore(dataDir, files)
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
	if n := len(res.Skipped); n > 0 {
		tail += fmt.Sprintf(", %d skipped (local-only path)", n)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deployed %d file(s) (%s%s) from %s workspace %q into %s.\n", res.Written, pinned, tail, server, sourceName, dataDir)
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
	// Authored areas resolve under DataDir; stories (attachment cache) under RuntimeDir
	// (sty_4660bbe1). Never derive either from DBPath — the planes diverge after migrate.
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = a.Config.ResolveDataDir(a.RepoRoot)
	}
	runtimeDir := a.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = a.Config.ResolveRuntimeDir(a.RepoRoot).Dir
	}
	switch area {
	case "constitution":
		return a.Config.ResolveConstitution(a.RepoRoot), false
	case "agents":
		return filepath.Join(dataDir, config.AgentsConfigName), false
	case "tasks":
		return filepath.Join(dataDir, "tasks"), true
	case "stories":
		return filepath.Join(runtimeDir, "stories"), true
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
