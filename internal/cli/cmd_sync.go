package cli

import (
	"crypto/sha256"
	"encoding/hex"
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
Files whose name carries a .local segment (satelle.local.toml, notes.local.md)
never leave the machine at any scope — unconditional, not a setting.
Personal opt-in targets this repo's bound hosted project only (never a shared
dump across all personal projects under your account). Bind with "satelle
project bind <slug>" (or set [sync] project in .satelle/satelle.toml). Team
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
		Use:   "scopes",
		Short: "Print each .satelle area's resolved scope, and shared files within a personal area",
		Long: `Print the resolved sync scope for every .satelle area — off, personal or team —
and, inside a personal area, which files are nonetheless shared.

Read this before a sync, not after: scope is what decides whether a file leaves
the machine, and the per-file exceptions inside a personal area are exactly what
a whole-area reading gets wrong. Read-only.`,
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
       creates .satelle/satelle.toml with [sync] project when absent
  4. satelle sync rehydrate   — this command, which runs:
       a. config deploy       — restores substrate AND satelle.toml (the settings
          area, including [sync] scopes) from the bound project's personal config
          collection; the local [sync] project binding is preserved (never
          taken from the deployed file)
       b. documents pull
       c. workstate pull      — stories/executions/ledger into the local store

Scopes live in satelle.toml and are restored by step 4a when a machine that had
[sync] settings (or all) = personal previously pushed. If the hosted project has
no satelle.toml yet (never pushed from a post-0.0.330 binary with settings
opted in), areas stay local and rehydrate names the fix.

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
	outcome, err := runSyncConfigDeployOutcome(cmd, serverArg, "", 0)
	if err != nil {
		return err
	}
	// After deploy, if every sync area is still local, explain the real cause
	// (settings not restored vs restored-but-all-local).
	if note := rehydrateAllLocalNote(outcome.SettingsRestored); note != "" {
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
	fmt.Fprintln(out, rehydrateDoneSummary(outcome))
	return nil
}

// rehydrateDoneSummary is the truthful closing line for rehydrate: what was
// and was not restored (AC4).
func rehydrateDoneSummary(o deployOutcome) string {
	settings := "settings: not restored"
	if o.SettingsRestored {
		settings = "settings: restored"
		if scopes := scopeSummary(); scopes != "" {
			settings += " (scopes now: " + scopes + ")"
		}
	}
	return fmt.Sprintf("rehydrate: done — %s | config files: %d | (documents + workstate reported above)",
		settings, o.Written)
}

// scopeSummary renders non-local areas as "area=scope, …" for the rehydrate
// closing line. Empty when load fails or every area is local.
func scopeSummary() string {
	cfg, _, _, err := loadRepoConfig()
	if err != nil {
		return ""
	}
	var parts []string
	for _, area := range config.SyncAreas {
		scope, serr := config.ScopeFor(cfg, area)
		if serr != nil || scope == config.LocalScope {
			continue
		}
		parts = append(parts, area+"="+scope.String())
	}
	return strings.Join(parts, ", ")
}

// rehydrateAllLocalNote returns an operator-facing explanation when every
// [sync] area is still local after config deploy. Empty when at least one area
// is personal|shared. Wording distinguishes "settings not restored from hosted"
// from "restored and genuinely all-local" (AC5).
func rehydrateAllLocalNote(settingsRestored bool) string {
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
	if !settingsRestored {
		return "rehydrate: note — satelle.toml was not restored from hosted (the bound project has no settings/satelle.toml, or it was never pushed from a machine with [sync] settings or all = personal). Documents and workstate pulls will no-op until you push config from a machine that has content (`satelle sync config push`) or hand-set [sync] here once."
	}
	return "rehydrate: note — satelle.toml was restored and every area is still local (the hosted scopes genuinely opt nothing in). Documents and workstate pulls will no-op until [sync] opts an area into personal."
}

// deployOutcome is the structured result of a config deploy so rehydrate can
// report facts rather than guess (sty_ea18294f AC4).
type deployOutcome struct {
	Written          int
	Missing          int
	SkippedLocal     int
	SettingsRestored bool
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
	// The documents PULL stays in bare sync — deliberately, unlike config deploy
	// and workstate pull, which sit behind `sync rehydrate` (sty_4c3729e7 AC7).
	// The difference is what each one does to local state. Deploy and workstate
	// pull MATERIALISE a partition over local files; they answer "make this repo
	// look like that one", so they are recovery, not routine. The documents pull
	// is INCREMENTAL and cursor-driven: it applies only what changed since this
	// repo's own cursor, and after this story it neither rewrites bytes it
	// already has nor stops on a file it cannot write. That makes documents the
	// one area that is genuinely two-way in normal use — an operator writing a
	// document on another machine expects it here without invoking recovery.
	// Moving it behind rehydrate would make routine convergence an explicit
	// recovery step, which is the wrong default for the only bidirectional area.
	if dryRun {
		fmt.Fprintln(out, "documents pull: skipped under --dry-run (pull has no preview).")
	} else if err := runSyncDocumentsPull(cmd, serverArg, ""); err != nil {
		return err
	}
	return runSyncWorkstatePush(cmd, serverArg, dryRun, false)
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
		Long: `Move authored config between this repo and its bound hosted project: push what
you have, or deploy what the project holds.

Nothing leaves the machine by default — a hosted write happens only when the
area is opted in, so running this in a repo that never opted in is a no-op by
design rather than an error. Deploy OVERWRITES the local authored copy.`,
	}

	var pushServer, pushWorkspace string
	var dryRun bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload authored config to the versioned store (a new version per file)",
		Long: `push walks the authored-config areas (workflows/principles/skills/constitution/
agents/tasks/settings) per their resolved [sync] scope — skipping local areas — and uploads
each file as a new version into this repo's bound hosted PROJECT's personal
collection only (epic:sync-publish). The settings area is satelle.toml (including
[sync] scopes); [sync] project is redacted at push so a deploy cannot rebind
another repo. Identical content is idempotent (no new version). Files with a
.local segment are never uploaded (reported as withheld). Team is not a sync
destination; use satelle publish to expose artifacts to a team catalog.
Requires "satelle project bind <slug>".`,
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
writes every file byte-for-byte into this repo's data dir — including satelle.toml
(settings) with its [sync] scopes when present. The local [sync] project
binding is preserved after restore (never taken from the deployed file).
--version N pins a per-file version (default: latest). This is the "set up
project X like project Y" operation for process config. Requires "satelle
project bind <slug>". Local [sync] areas still default to writing nothing hosted
on push; deploy always materializes from the bound project partition.`,
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
	bundle, err := config.ConfigFiles(cfg, repoRoot)
	if err != nil {
		return err
	}
	files := bundle.Files
	out := cmd.OutOrStdout()
	printWithheldLocal := func() {
		for _, p := range bundle.Withheld {
			fmt.Fprintf(out, "  withheld (never syncs, .local): %s\n", p)
		}
	}
	if len(files) == 0 {
		if len(bundle.Withheld) > 0 {
			printWithheldLocal()
			fmt.Fprintf(out, "nothing to push (%d withheld)\n", len(bundle.Withheld))
			return nil
		}
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
		printWithheldLocal()
		return nil
	}
	// Bound project before any network (AC5).
	project, err := resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return err
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	// Prefer skipping unchanged bytes via server manifest (sty_88e83180 AC6).
	// On manifest failure: degrade to full upload with a printed note.
	headSHA := map[string]string{}
	if manifest, merr := client.ConfigManifest(cmd.Context(), project); merr != nil {
		if errors.Is(merr, hosted.ErrLoginRequired) {
			return merr
		}
		fmt.Fprintf(out, "config manifest unavailable (%v) — uploading every file.\n", merr)
	} else {
		headSHA = headSHAByPath(manifest)
	}
	var created, unchanged, notUploaded int
	for _, f := range files {
		if contentMatchesSHA(headSHA[f.Path], f.Content) {
			notUploaded++
			continue
		}
		res, perr := client.PushConfigFile(cmd.Context(), project, f.Path, f.Content)
		if perr != nil {
			if errors.Is(perr, hosted.ErrLoginRequired) {
				return perr
			}
			return fmt.Errorf("push %s: %w", f.Path, perr)
		}
		if res.Created {
			created++
		} else {
			unchanged++
		}
	}
	printWithheldLocal()
	uploaded := created + unchanged
	fmt.Fprintf(out, "Pushed %d of %d config file(s) to project %q personal collection on %s: %d new, %d unchanged, %d skipped (unchanged, not uploaded).\n",
		uploaded, len(files), project, server, created, unchanged, notUploaded)
	return nil
}

// headSHAByPath indexes ConfigItem.BlobSHA256 by path.
func headSHAByPath(items []hosted.ConfigItem) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		if it.Path != "" && it.BlobSHA256 != "" {
			out[it.Path] = it.BlobSHA256
		}
	}
	return out
}

// contentMatchesSHA reports whether content's sha256 matches the server value.
// Normalises quotes and an optional sha256: prefix; unknown formats return
// false so we never skip on doubt (sty_88e83180 AC6).
func contentMatchesSHA(sha string, content []byte) bool {
	sha = strings.TrimSpace(sha)
	sha = strings.Trim(sha, `"'`)
	sha = strings.TrimPrefix(sha, "sha256:")
	sha = strings.ToLower(sha)
	if len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]) == sha
}

func runSyncConfigDeploy(cmd *cobra.Command, serverArg, workspaceArg string, version int) error {
	_, err := runSyncConfigDeployOutcome(cmd, serverArg, workspaceArg, version)
	return err
}

func runSyncConfigDeployOutcome(cmd *cobra.Command, serverArg, workspaceArg string, version int) (deployOutcome, error) {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return deployOutcome{}, err
	}
	server := resolveServer(serverArg)
	if server == "" {
		return deployOutcome{}, fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	// Project-addressed config routes (sty_ca64d0cb). --workspace is accepted
	// but inert for routing (workspace is derived from the project server-side);
	// keep it in user-facing messages for the deprecation window.
	project, err := resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return deployOutcome{}, err
	}
	sourceName := resolveDeploySourceName(cfg, workspaceArg)
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	manifest, err := client.ConfigManifest(cmd.Context(), project)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return deployOutcome{}, err
		}
		return deployOutcome{}, fmt.Errorf("fetch manifest: %w", err)
	}
	out := cmd.OutOrStdout()
	if len(manifest) == 0 {
		fmt.Fprintf(out, "No config in workspace %q project %q on %s.\n", sourceName, project, server)
		return deployOutcome{}, nil
	}
	var files []subsync.File
	var missing int
	var settingsInManifest bool
	for _, item := range manifest {
		if item.Path == config.ConfigName {
			settingsInManifest = true
		}
		content, _, ferr := client.ConfigFileContent(cmd.Context(), project, item.Path, version)
		if ferr != nil {
			if errors.Is(ferr, hosted.ErrConfigFileMissing) && version > 0 {
				missing++ // a file this version predates — skip it
				continue
			}
			if errors.Is(ferr, hosted.ErrLoginRequired) {
				return deployOutcome{}, ferr
			}
			return deployOutcome{}, fmt.Errorf("fetch %s: %w", item.Path, ferr)
		}
		files = append(files, subsync.File{Path: item.Path, Content: content})
	}
	if len(files) == 0 {
		fmt.Fprintf(out, "Nothing to deploy — version %d matched no files in workspace %q.\n", version, sourceName)
		return deployOutcome{}, nil
	}
	// Capture the local binding before restore; re-apply after so a hosted
	// satelle.toml (even an older unredacted one) cannot rebind this repo.
	boundProject := project
	cfgPath := filepath.Join(dataDir, config.ConfigName)
	res, err := subsync.Restore(dataDir, files)
	if err != nil {
		return deployOutcome{}, fmt.Errorf("deploy: %w", err)
	}
	// A deploy materialises a whole partition deliberately — a file it could not
	// write is a FAILED deploy, not a partial one. Restore stopped hard-erroring
	// on an unwritable file so a cursor-driven pull can continue past it
	// (sty_4c3729e7); this caller keeps the loud behaviour it always had.
	if err := res.Err(); err != nil {
		return deployOutcome{}, fmt.Errorf("deploy: %w", err)
	}
	outcome := deployOutcome{
		Written:      res.Written,
		Missing:      missing,
		SkippedLocal: len(res.Skipped),
	}
	if settingsInManifest {
		// Did Restore actually write satelle.toml (not skip it as local-only)?
		settingsWritten := true
		for _, sk := range res.Skipped {
			if sk == config.ConfigName {
				settingsWritten = false
				break
			}
		}
		if settingsWritten {
			outcome.SettingsRestored = true
			if err := config.SaveConfigValues(cfgPath, []config.KeyEdit{
				config.BoundProjectEdit(boundProject),
			}); err != nil {
				return outcome, fmt.Errorf("deploy: preserve [sync] project: %w", err)
			}
			fmt.Fprintf(out, "settings: satelle.toml restored; [sync] project preserved as %q (not taken from the deployed file).\n", boundProject)
		}
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
	fmt.Fprintf(out, "Deployed %d file(s) (%s%s) from %s workspace %q into %s.\n", res.Written, pinned, tail, server, sourceName, dataDir)
	return outcome, nil
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
		// Local areas push nothing — nothing to report. Non-local areas can leave
		// the machine, so report both shared: promotions and withheld .local files.
		if scope == config.LocalScope {
			continue
		}
		path, isDir := syncAreaPath(a, area)
		if path == "" {
			continue
		}
		shared, withheld, err := areaFileNotes(path, isDir, scope)
		if err != nil {
			return fmt.Errorf("sync area %q: %w", area, err)
		}
		for _, f := range shared {
			fmt.Fprintf(w, "\tshared: %s\n", f)
		}
		for _, f := range withheld {
			fmt.Fprintf(w, "\twithheld: %s\n", f)
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
		path, _ := config.AgentsPath(dataDir)
		return path, false
	case "tasks":
		return filepath.Join(dataDir, "tasks"), true
	case "settings":
		return filepath.Join(dataDir, config.ConfigName), false
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

// areaFileNotes walks an area's on-disk location and returns two lists:
//   - shared: markdown files with frontmatter shared:true (personal-scope only;
//     same filter as the historical sharedFilesIn)
//   - withheld: any file whose basename/path carries a .local segment, every
//     extension, so operators see what the bundler will keep back
//
// A non-existent path is benign (empty results, not an error).
func areaFileNotes(path string, isDir bool, scope config.Scope) (shared, withheld []string, err error) {
	noteOne := func(p string) error {
		base := filepath.Base(p)
		// Basename is enough: LocalOnlyPath matches on any segment's .local
		// component (directory segments like secrets.local/ are covered when
		// walking files under them, since each file path is checked fully).
		if config.LocalOnlyPath(filepath.ToSlash(p)) || config.LocalOnlyPath(base) {
			withheld = append(withheld, p)
			return nil // withheld files are not also listed as shared
		}
		if scope != config.PersonalScope {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		name := strings.TrimSuffix(base, filepath.Ext(p))
		if name == "index" || name == "log" || strings.EqualFold(name, "README") {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if config.FileShared(config.PersonalScope, string(body)) {
			shared = append(shared, p)
		}
		return nil
	}
	if !isDir {
		if _, serr := os.Stat(path); os.IsNotExist(serr) {
			return nil, nil, nil
		}
		if err := noteOne(path); err != nil {
			return nil, nil, err
		}
		return shared, withheld, nil
	}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) && p == path {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			// Descend into every directory; LocalOnlyPath matches each file.
			return nil
		}
		return noteOne(p)
	})
	if err != nil {
		return nil, nil, err
	}
	return shared, withheld, nil
}
