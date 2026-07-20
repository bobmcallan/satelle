// `satelle workspace` manages the connected-repo registry the /workspace view
// aggregates (build order step 6). The registry lives in the global config
// (~/.satelle/config.toml [workspace] repos); per-repo databases stay the source
// of truth. `workspace add` also seeds the push-fed serve mirror, bootstrapping
// [server] endpoint into satelle.local.toml when a local serve is reachable
// (sty_805bee9c / sty_0122610a / epic:serve-adoption).
//
// Mirror hygiene (epic:mirror-hygiene / sty_5aa08259 / sty_eb61be02):
//   - SATELLE_SERVER_ENDPOINT=none disables discovery and push (hermetic tests).
//   - Auto-bootstrap only adopts a serve whose X-Satelle-Instance matches this
//     SATELLE_HOME — foreign :8787 answers are not seeded.
//   - workspace remove purges the mirror partition; partitions / prune manage
//     orphans (test pollution).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
)

func init() {
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Manage the connected-repo registry the workspace view aggregates",
	}

	add := &cobra.Command{
		Use:   "add [path]",
		Short: "Join the workspace: register, seed the serve mirror (bootstraps [server] endpoint when a matching local serve is running)",
		Long: `workspace add registers a repo in the machine-local workspace registry
(~/.satelle/config.toml [workspace] repos) and seeds the push-fed serve mirror
so the project appears on the landing.

[server] endpoint is required to seed. It usually lives in the gitignored
per-machine .satelle/satelle.local.toml (not committed satelle.toml). When no
endpoint is set and a local serve answers at the global service port (default
http://127.0.0.1:8787) WITH a matching X-Satelle-Instance for this SATELLE_HOME,
workspace add writes that endpoint into local.toml and seeds in the same command.
A value already present in satelle.toml or local.toml is respected and not
overwritten. SATELLE_SERVER_ENDPOINT overrides: a URL forces that endpoint; none|off|-
disables discovery and push (used by hermetic tests).

If no endpoint is set and no matching serve answers, the verb still registers
the path, prints that seed was skipped, and exits 0 — re-run after starting
serve, or set [server] endpoint.

The landing URL slug is the repo directory's basename (e.g. /r/my-app/ for a
repo at …/my-app). Seeding a second repo whose basename already belongs to
another partition is rejected (HTTP 409) with a message naming the conflict —
rename the directory so basenames are unique and re-run. There is no
slug-override flag today.

This is the single join verb for the local UI (epic:serve-adoption). Later
mutations are pushed automatically by the CLI change publisher — re-run
workspace add only to re-seed the mirror manually.

Path defaults to the current directory. When the path is a different repo than
the one opened by the CLI, only registration runs — seed from inside that repo.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE:        runWorkspaceAdd,
	}

	remove := &cobra.Command{
		Use:   "remove [path]",
		Short: "Unregister a repo and purge its mirror partition (defaults to the current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runWorkspaceRemove,
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered repos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gc, err := config.LoadGlobal()
			if err != nil {
				return err
			}
			if len(gc.Workspace.Repos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no repos registered — add one with `satelle workspace add`")
				return nil
			}
			for _, r := range gc.Workspace.Repos {
				fmt.Fprintln(cmd.OutOrStdout(), r)
			}
			return nil
		},
	}

	parts := &cobra.Command{
		Use:   "partitions",
		Short: "List mirror partitions (repo_key, slug, path, counts) on the local serve",
		Args:  cobra.NoArgs,
		RunE:  runWorkspacePartitions,
	}

	var pruneForce bool
	prune := &cobra.Command{
		Use:   "prune <repo_key>",
		Short: "Remove a mirror partition by repo_key (orphans from tests; --force if path still exists)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspacePrune(cmd, args[0], pruneForce)
		},
	}
	prune.Flags().BoolVar(&pruneForce, "force", false, "allow prune when the partition's repo path still exists on disk")

	ws.AddCommand(add, remove, list, parts, prune)
	register(ws)
}

// runWorkspaceAdd registers the path then seeds the mirror when endpoint is set
// (or can be bootstrapped from a matching live local serve) and the path is the
// active repo (sty_805bee9c / sty_0122610a / sty_5aa08259).
func runWorkspaceAdd(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	// Default path is the opened repo (config-resolved), not process cwd — so
	// SATELLE_CONFIG / sitting in a subdir still seeds the right store.
	var path string
	if len(args) == 1 {
		path, err = filepath.Abs(args[0])
		if err != nil {
			return err
		}
	} else {
		path, err = filepath.Abs(a.RepoRoot)
		if err != nil {
			return err
		}
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	added := gc.Workspace.AddRepo(path)
	if added {
		if err := config.SaveGlobal(gc); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "added %s (%d repos)\n", path, len(gc.Workspace.Repos))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s already registered\n", path)
	}

	// Snapshot only the active repo's store — other paths are register-only.
	activeRoot, _ := filepath.Abs(a.RepoRoot)
	if filepath.Clean(path) != filepath.Clean(activeRoot) {
		fmt.Fprintf(cmd.OutOrStdout(), "workspace add: registered %s; run `satelle workspace add` from inside that repo to seed its mirror\n", path)
		return nil
	}
	ep, bootstrapped, skipReason, err := resolveSeedEndpoint(a, gc)
	if err != nil {
		return err
	}
	if ep == "" {
		fmt.Fprintln(cmd.OutOrStdout(), seedSkipNotice(skipReason))
		return nil
	}
	if bootstrapped {
		fmt.Fprintf(cmd.OutOrStdout(), "workspace add: bootstrapped [server] endpoint = %q into %s\n",
			ep, localTomlPath(a))
	}
	snap, err := buildUISnapshot(cmd.Context(), a)
	if err != nil {
		return err
	}
	if err := postUISnapshot(ep, snap); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "workspace add: ok repo_key=%s stories=%d tasks=%d → %s/ingest/snapshot\n",
		snap.RepoKey, len(snap.Stories), len(snap.Tasks), strings.TrimRight(ep, "/"))
	return nil
}

// resolveSeedEndpoint returns the serve base URL to seed against.
// Design (sty_5aa08259, recorded in-story): BOTH an explicit off-switch
// (SATELLE_SERVER_ENDPOINT=none) AND instance identity on auto-bootstrap.
// Empty endpoint with skipReason is not an error — registration already
// succeeded (AC3: exit 0, seed skipped).
func resolveSeedEndpoint(a *app.App, gc config.GlobalConfig) (endpoint string, bootstrapped bool, skipReason string, err error) {
	if ep, disabled, set := resolveServerEndpointEnv(); set {
		if disabled {
			return "", false, EnvServerEndpoint + "=none (discovery and push disabled)", nil
		}
		return ep, false, "", nil
	}
	if ep := strings.TrimSpace(a.Config.Server.Endpoint); ep != "" {
		return ep, false, "", nil
	}
	candidate := defaultServerEndpoint(gc)
	want := config.CurrentInstanceID()
	hz := strings.TrimRight(candidate, "/") + "/healthz"
	if !healthzInstanceOK(hz, want) {
		// Distinguish unreachable vs foreign instance for the skip message.
		if !healthzOK(hz) {
			return "", false, fmt.Sprintf(
				"no [server] endpoint and no serve answered %s — start serve then re-run, or set endpoint in %s",
				hz, localTomlPath(a)), nil
		}
		return "", false, instanceMismatchReason(candidate, want), nil
	}
	localPath := localTomlPath(a)
	edit := config.KeyEdit{Section: "server", Key: "endpoint", Value: strconv.Quote(candidate)}
	if err := config.SaveConfigValues(localPath, []config.KeyEdit{edit}); err != nil {
		return "", false, "", fmt.Errorf("workspace add: bootstrap [server] endpoint into %s: %w", localPath, err)
	}
	// Keep the in-memory config in step so any same-process reader sees it.
	a.Config.Server.Endpoint = candidate
	return candidate, true, "", nil
}

// defaultServerEndpoint is the bootstrap candidate from the global service port
// (DefaultWebPort when unset). Always 127.0.0.1 — the CLI publishes to a local
// serve, not a remote bind address.
func defaultServerEndpoint(gc config.GlobalConfig) string {
	return fmt.Sprintf("http://127.0.0.1:%d", gc.Service.ResolvePort())
}

// localTomlPath is the gitignored per-machine overlay beside the committed
// satelle.toml for the active repo.
func localTomlPath(a *app.App) string {
	return filepath.Join(a.DataDir, config.LocalConfigName)
}

// resolveRepoArg returns the absolute repo path from an optional arg, defaulting
// to the current directory.
func resolveRepoArg(args []string) (string, error) {
	p := "."
	if len(args) == 1 {
		p = args[0]
	}
	return filepath.Abs(p)
}

func runWorkspaceRemove(cmd *cobra.Command, args []string) error {
	path, err := resolveRepoArg(args)
	if err != nil {
		return err
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	repoKey := config.RepoKey(path)
	removed := gc.Workspace.RemoveRepo(path)
	if removed {
		if err := config.SaveGlobal(gc); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed %s from registry (%d repos left)\n", path, len(gc.Workspace.Repos))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s was not registered\n", path)
	}

	ep, epSrc := resolveOperatorEndpoint(cmd, path)
	if ep != "" {
		if err := postUIRemove(ep, repoKey); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "notice: registry updated but mirror partition purge failed (%v) — run `satelle workspace prune %s`\n",
				err, repoKey)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "purged mirror partition repo_key=%s via %s/ingest/remove\n",
			repoKey, strings.TrimRight(ep, "/"))
		return nil
	}
	// No matching serve: purge local mirror store so the landing card disappears
	// even when the unit is pre-identity or down (sty_eb61be02).
	ms, err := mirror.Open(mirror.DefaultPath(config.GlobalDir()))
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "notice: registry updated; could not open local mirror (%v) — %s\n", err, epSrc)
		return nil
	}
	defer ms.Close()
	if err := ms.DeletePartition(cmd.Context(), repoKey); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "notice: registry updated; local partition purge failed (%v)\n", err)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "purged mirror partition repo_key=%s from local store (no matching serve — %s)\n",
		repoKey, epSrc)
	return nil
}

func runWorkspacePartitions(cmd *cobra.Command, args []string) error {
	// List always reads the local mirror plane (same GlobalDir as serve when
	// co-located). No serve endpoint required — operators repair pollution
	// even when the unit is down or still on a pre-identity binary.
	ms, err := mirror.Open(mirror.DefaultPath(config.GlobalDir()))
	if err != nil {
		return fmt.Errorf("workspace partitions: open mirror: %w", err)
	}
	defer ms.Close()
	details, err := ms.ListPartitionDetails(cmd.Context())
	if err != nil {
		return err
	}
	if len(details) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no mirror partitions")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%-40s %-20s %6s %5s %4s  %s\n", "REPO_KEY", "SLUG", "STORY", "TASK", "DOC", "PATH")
	for _, d := range details {
		path := d.Path
		if path == "" {
			path = "-"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-40s %-20s %6d %5d %4d  %s\n",
			d.RepoKey, d.Slug, d.Stories, d.Tasks, d.Docs, path)
	}
	return nil
}

func runWorkspacePrune(cmd *cobra.Command, repoKey string, force bool) error {
	repoKey = strings.TrimSpace(repoKey)
	if repoKey == "" {
		return fmt.Errorf("workspace prune: repo_key required")
	}
	ms, err := mirror.Open(mirror.DefaultPath(config.GlobalDir()))
	if err != nil {
		return fmt.Errorf("workspace prune: open mirror: %w", err)
	}
	defer ms.Close()
	details, err := ms.ListPartitionDetails(cmd.Context())
	if err != nil {
		return err
	}
	var found *mirror.PartitionDetail
	for i := range details {
		if details[i].RepoKey == repoKey {
			found = &details[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("workspace prune: unknown repo_key %q — run `satelle workspace partitions`", repoKey)
	}
	if path := strings.TrimSpace(found.Path); path != "" && !force {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return fmt.Errorf("workspace prune: path %s still exists — pass --force to remove the partition anyway, or use `satelle workspace remove` from that repo", path)
		}
	}
	ep, epSrc := resolveOperatorEndpoint(cmd, "")
	if ep != "" {
		// Prefer ingest path so a live serve's in-memory/WAL view updates and
		// SSE rings. Close our handle first to reduce lock contention.
		_ = ms.Close()
		if err := postUIRemove(ep, repoKey); err != nil {
			return fmt.Errorf("workspace prune via %s: %w (source %s)", ep, err, epSrc)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "pruned partition repo_key=%s via %s/ingest/remove\n",
			repoKey, strings.TrimRight(ep, "/"))
		return nil
	}
	// No matching serve (down, foreign instance, or pre-identity binary): delete
	// from the local mirror store directly. The live unit will see the purge on
	// next open/query of the shared DB file (WAL-friendly).
	if err := ms.DeletePartition(cmd.Context(), repoKey); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "pruned partition %s from local mirror store (no matching serve endpoint — %s)\n",
		repoKey, epSrc)
	return nil
}

// resolveOperatorEndpoint picks a serve base URL for remove/prune/list side
// effects. Prefer env, then opened app config when available, then default
// probe with instance match.
func resolveOperatorEndpoint(cmd *cobra.Command, hintPath string) (endpoint, source string) {
	if ep, disabled, set := resolveServerEndpointEnv(); set {
		if disabled {
			return "", EnvServerEndpoint + " disables push"
		}
		return ep, EnvServerEndpoint
	}
	if a, err := appFrom(cmd); err == nil && a != nil {
		if ep := strings.TrimSpace(a.Config.Server.Endpoint); ep != "" {
			return ep, "[server] endpoint"
		}
	}
	// Optional: load repo config (committed + local overlay) from hintPath.
	if hintPath != "" {
		committed := filepath.Join(hintPath, ".satelle", config.ConfigName)
		if cfg, _, err := config.Load(committed); err == nil {
			if ep := strings.TrimSpace(cfg.Server.Endpoint); ep != "" {
				return ep, committed
			}
		}
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		return "", "no global config"
	}
	candidate := defaultServerEndpoint(gc)
	want := config.CurrentInstanceID()
	hz := strings.TrimRight(candidate, "/") + "/healthz"
	if healthzInstanceOK(hz, want) {
		return candidate, "live serve " + candidate
	}
	return "", "no matching serve at " + candidate
}

// postUIRemove POSTs /ingest/remove for repoKey.
func postUIRemove(endpoint, repoKey string) error {
	return postUIRemoveContext(context.Background(), endpoint, repoKey)
}

func postUIRemoveContext(ctx context.Context, endpoint, repoKey string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	body, err := json.Marshal(mirror.RemoveEvent{RepoKey: repoKey})
	if err != nil {
		return err
	}
	url := strings.TrimRight(strings.TrimSpace(endpoint), "/") + "/ingest/remove"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}
