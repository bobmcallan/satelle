package cli

// `satelle sync push` and `satelle sync pull` (sty_0fa4f732): the git-aligned
// verbs over the substrate. Push publishes every synced area through the one
// push composition bare sync runs. Pull brings in the areas that are only behind
// the hosted copy. What decides a state lives in snapsync.Driver.Status, reached
// through probeSubstrate, and what a pull does to a file lives in the
// area-restricted snapshot apply reconcile shares (applySnapshotAreas); this file
// decides neither, and only chooses which areas to hand the apply and reports.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
)

// The command groups `sync --help` lists: the git-aligned verbs first, then the
// per-area and recovery commands.
const (
	syncVerbsGroup = "verbs"
	syncAreaGroup  = "areas"
)

func newSyncPushCmd() *cobra.Command {
	var server string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish this machine's changes: config, documents and work-state (no pull)",
		Long: `push publishes every area to the bound hosted project, under the resolved
scope (nothing when it is local): authored config,
documents, then work-state. It is the push half of bare "satelle sync" and pulls
nothing; to bring in what only the hosted copy changed, run "satelle sync pull".

A push is refused while the hosted copy is ahead of this machine (the refusal
names "satelle sync pull" for an area that is only behind and "satelle sync
reconcile" for one changed on both machines) or while a conflict copy is
unmerged. --dry-run, --force, --prune and --server mean what they mean on the
per-area pushes.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncPush(cmd, server, dryRun)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what each area would push; only the scope is asked of the account.")
	cmd.Flags().Bool("prune", false, pruneFlagUsage)
	cmd.Flags().Bool("force", false, forceFlagUsage)
	return cmd
}

func runSyncPush(cmd *cobra.Command, serverArg string, dryRun bool) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	if scope, err := syncScopeGate(cmd, a.Config, a.RepoRoot, resolveServer(serverArg), "Nothing to push"); err != nil || scope == config.LocalScope {
		return err
	}
	return runSyncPushLegs(cmd, serverArg, dryRun, nil)
}

func newSyncPullCmd() *cobra.Command {
	var server string
	var fastForwardOnly bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Bring in what only the hosted copy has changed; never overwrites a local edit",
		Long: `pull brings in the config areas and documents that "satelle sync status" reports
behind, when the pull changes nothing this machine changed. Any other area is left
byte-for-byte as it was and reported with its next command: unpushed or never
synced, "satelle sync push"; diverged or unmerged, "satelle sync reconcile".
It never pushes a local file or touches work-state; hosted files with no snapshot, and
nothing local, are first settled into a snapshot, then pulled.

Exits non-zero when any area is diverged, unmerged, unavailable or could not be
pulled; zero otherwise.

--fast-forward-only applies the same rule and prints only the areas pulled or
turned away; satelled runs it after its push. "satelle sync rehydrate" is the
full-tree recovery that overwrites local files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncPull(cmd, server, fastForwardOnly)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	cmd.Flags().BoolVar(&fastForwardOnly, "fast-forward-only", false, "Print only the areas pulled or turned away (what satelled runs); the rule is the same as without it.")
	return cmd
}

// syncPullBeforeApply is a test seam: it runs after the areas are classified and
// before any is planned for writing, which is where an edit can slip in.
var syncPullBeforeApply func()

// runSyncPull classifies every synced area with the probe `sync status` runs,
// pulls the ones that are behind through the area-restricted apply reconcile
// shares, and reports the rest with their next command. Plain pull and
// --fast-forward-only apply one rule; the flag only quiets the report.
func runSyncPull(cmd *cobra.Command, serverArg string, fastForwardOnly bool) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	server := resolveServer(serverArg)
	scope, err := syncScopeGate(cmd, cfg, repoRoot, server, "Nothing to pull")
	if err != nil || scope == config.LocalScope {
		return err
	}
	areas := config.SubstrateAreas(scope)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return err
	}
	client := newHostedClient(cmd.Context(), server, repoRoot)
	statuses := probeSubstrate(cmd.Context(), cfg, repoRoot, dataDir, server, project, client, areas)
	recordSubstrateStates(repoRoot, statuses)
	if syncPullBeforeApply != nil {
		syncPullBeforeApply()
	}

	rc := &reconcileApply{areas: map[string]bool{}, fastForward: true, refused: map[string]string{}}
	refusedNext := map[string]string{}
	if err := settleHeadsOnly(cmd, cfg, repoRoot, dataDir, server, project, client, statuses, rc, refusedNext); err != nil {
		return fmt.Errorf("sync pull: %w", err)
	}
	var behind []string
	documents := false
	for _, st := range statuses {
		if st.State != snapsync.StateBehind || rc.refused[st.Area] != "" {
			continue
		}
		behind = append(behind, st.Area)
		if st.Area == "documents" {
			documents = true
		} else {
			rc.areas[st.Area] = true
		}
	}
	// The apply plans each behind area again at the point of writing and turns
	// away any that is no longer a fast-forward (see reconcileApply.admit).
	if len(behind) > 0 {
		if err := applySnapshotAreas(cmd, cfg, repoRoot, dataDir, server, project, serverArg, rc, documents); err != nil {
			return fmt.Errorf("sync pull: %w", err)
		}
	}
	now := map[string]snapsync.AreaStatus{}
	if len(behind) > 0 {
		for _, st := range probeSubstrate(cmd.Context(), cfg, repoRoot, dataDir, server, project, client, behind) {
			now[st.Area] = st
		}
	}

	var failed []string
	// An area with nothing to pull is a no-op success; only the full report says so.
	noop := func(format string, a ...any) {
		if !fastForwardOnly {
			fmt.Fprintf(out, format+"\n", a...)
		}
	}
	for _, st := range statuses {
		switch st.State {
		case snapsync.StateInStep:
			noop("%s: in step — nothing to pull", st.Area)
		case snapsync.StateUnpushed, snapsync.StateNeverSynced:
			if st.Effective == 0 {
				noop("%s: the hosted copy holds no snapshot yet — nothing to pull (%s publishes it)", st.Area, nextCommand(st))
			} else {
				noop("%s: %s — nothing to pull; next: %s", st.Area, st.State, nextCommand(st))
			}
		case snapsync.StateBehind:
			if why := rc.refused[st.Area]; why != "" {
				next := refusedNext[st.Area]
				if next == "" {
					next = "satelle sync reconcile"
				}
				fmt.Fprintf(out, "%s: behind, left unchanged — %s; next: %s\n", st.Area, why, next)
				failed = append(failed, st.Area+" is behind but "+why)
				continue
			}
			after := now[st.Area]
			if after.State != snapsync.StateInStep && after.State != snapsync.StateUnpushed {
				next := nextCommand(after)
				if after.State == snapsync.StateBehind && after.Effective == 0 {
					// Still no snapshot to pull: another pull would do what this one did.
					next = "check the server, then satelle sync status"
				}
				fmt.Fprintf(out, "%s: pulled what it could, now %s; next: %s\n", st.Area, after.State, next)
				failed = append(failed, fmt.Sprintf("%s is still %s after the pull", st.Area, after.State))
				continue
			}
			fmt.Fprintf(out, "%s: pulled — %s\n", st.Area, after.State)
		case snapsync.StateUnavailable:
			reason := "unavailable"
			if st.Err != nil {
				reason += " (" + st.Err.Error() + ")"
			}
			fmt.Fprintf(out, "%s: %s; next: %s\n", st.Area, reason, nextCommand(st))
			failed = append(failed, st.Area+" "+reason)
		default: // diverged, unmerged
			fmt.Fprintf(out, "%s: %s — left unchanged; next: %s\n", st.Area, st.State, nextCommand(st))
			failed = append(failed, st.Area+" "+st.State)
		}
	}
	if len(failed) > 0 {
		return errors.New("sync pull: " + strings.Join(failed, "; "))
	}
	return nil
}

// settleHeadsOnly gives each heads-only area its first snapshot, so the pull that
// follows has something to fast-forward onto. Such an area is one the hosted copy
// holds as per-file uploads with no snapshot (the plain-upload fallback), and this
// machine holds nothing of: behind, with no effective snapshot. PlanPull has no
// snapshot to plan from, so without this the pull changes nothing and status goes
// on naming a pull.
//
// The snapshot is the heads exactly as they stand, published through the one push
// composition (runSnapshotPush, snapshot-only so a server that cannot hold one is
// refused, never sent the plain upload). It is claimed only while the freshly read
// local set is empty, so the pull that follows can overwrite nothing: a file that
// appeared since the probe leaves the area untouched. An area it cannot settle is
// recorded in rc.refused, with the command that moves it on in refusedNext.
func settleHeadsOnly(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir, server, project string, client *hosted.Client, statuses []snapsync.AreaStatus, rc *reconcileApply, refusedNext map[string]string) error {
	var drivers *substrateDrivers
	for _, st := range statuses {
		if st.State != snapsync.StateBehind || st.Effective != 0 {
			continue
		}
		if drivers == nil {
			var err error
			if drivers, err = newSubstrateDrivers(cmd.Context(), cfg, repoRoot, dataDir, server, project, client); err != nil {
				return fmt.Errorf("fetch manifest: %w", err)
			}
		}
		area := st.Area
		d := drivers.forArea(area)
		local, err := pullLocalShas(cfg, repoRoot, server, project, d, area)
		if err != nil {
			return fmt.Errorf("%s: read the local files: %w", area, err)
		}
		if len(local) > 0 {
			rc.refused[area], refusedNext[area] = "a local file appeared since the status was read", "satelle sync status"
			continue
		}
		out, _, err := runSnapshotPush(cmd.Context(), d, map[string][]snapsync.PushFile{area: nil}, []string{area}, true)
		var unavailable *snapsync.UnavailableError
		var behind *snapsync.ErrBehind
		switch {
		case err == nil && out.claimed[area]:
		case err == nil, errors.As(err, &unavailable):
			// The read-back fallback claims nothing a reader can see; a server that
			// cannot read a snapshot cannot hold one.
			rc.refused[area], refusedNext[area] = "the hosted copy cannot hold a snapshot", "check the server, then satelle sync status"
		case errors.As(err, &behind) && (out.raced[area] || behind.Effective > 0):
			// A snapshot exists now — another machine's claim won. The apply plans
			// against it and fast-forwards or turns the area away.
		case errors.As(err, &behind):
			rc.refused[area], refusedNext[area] = "the hosted copy has no snapshot to pull", "satelle sync status"
		default:
			return fmt.Errorf("%s: %w", area, err)
		}
	}
	return nil
}
