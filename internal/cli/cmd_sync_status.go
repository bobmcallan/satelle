package cli

// Substrate continuity (sty_7562055c): where each authored area and the documents
// stand against the bound project's hosted copy. Everything that DECIDES a state
// lives in snapsync.Driver.Status, which asks the same push-planning path a push
// runs; this file builds the drivers, records what they answer and prints it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/verb"
)

func newSyncStatusCmd() *cobra.Command {
	var server, story string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print whether each synced area is in step with the bound project's hosted copy",
		Long: `status contacts the bound hosted project and prints, for every config area and
documents that [sync] opts in, its state — in step, unpushed, behind, diverged,
unmerged, never synced or unavailable — with the snapshot this checkout last
synced, the hosted snapshot and the next command to run.

The state comes from the planning a push runs, so a push that would be refused
shows as the reason. Exits non-zero when any area is not in step. Each verified
state is recorded, so "satelle doctor" and a new engagement can report it
without the server.

--story <id> checks only the areas that story changed: non-zero while any is
not on the hosted copy (in step and behind pass), zero and offline when it
touched no synced substrate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if story != "" {
				// The story's change sets live in the local store, which a bare
				// "sync status" never needs, so only this form opens it.
				if err := openAppForCmd(cmd); err != nil {
					return err
				}
				return runSyncStatusForStory(cmd, server, story)
			}
			return runSyncStatus(cmd, server)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	cmd.Flags().StringVar(&story, "story", "", "Check only the synced areas this story's change touched (non-zero while any is not on the hosted copy).")
	return cmd
}

// noEngagementBaseline is the text of story-diff's no-baseline answer: the story
// was never engaged, so its live leg is legitimately empty. Pinned by a test, as
// the verb package keeps the sentinel itself private.
const noEngagementBaseline = "no engagement baseline"

// storyTouchedPaths is the union of a story's recorded change sets and its live
// "story diff --include-substrate" leg, both asked of the story-diff verb. A
// story with no engagement baseline has no live leg; any other refusal (a foreign
// tree, git unavailable) is returned, because an unreadable change set must not
// read as "touched nothing". Worktree stories report their main-tree substrate
// edits relative to the process root, which is the root the caller maps against.
func storyTouchedPaths(ctx context.Context, storyID string) ([]string, error) {
	var paths []string
	for _, leg := range []map[string]any{{"recorded": true}, {"include_substrate": true}} {
		leg["id"] = storyID
		body, err := json.Marshal(leg)
		if err != nil {
			return nil, err
		}
		raw, err := verb.Dispatch(ctx, "story-diff", body)
		if err != nil {
			if strings.Contains(err.Error(), noEngagementBaseline) {
				continue
			}
			return nil, fmt.Errorf("story diff %s: %w", storyID, err)
		}
		var d struct {
			Files            []string `json:"files"`
			ProcessRootFiles []string `json:"process_root_files"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("story diff %s: %w", storyID, err)
		}
		paths = append(paths, d.Files...)
		paths = append(paths, d.ProcessRootFiles...)
	}
	return paths, nil
}

// loadProcessRepoConfig is loadRepoConfig for the process of record: the main
// tree's config and root when this is a linked worktree, where the substrate
// lives (".satelle" is never in the worktree). In the main tree the two agree.
func loadProcessRepoConfig() (config.Config, string, string, error) {
	loc, locPath, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return config.Config{}, "", "", fmt.Errorf("load config: %w", err)
	}
	proc, root, _, err := config.LoadProcess(loc, locPath)
	if err != nil {
		return config.Config{}, "", "", fmt.Errorf("load config: %w", err)
	}
	return proc, root, proc.ResolveDataDir(root), nil
}

// runSyncStatusForStory checks the substrate one story changed against the hosted
// copy. Which areas the story touched comes from the story-diff verb mapped
// through config.AreaOfRepoPath; whether each is on the hosted copy comes from the
// same probe plain "sync status" runs (snapsync.Driver.Status) and the remedy
// from nextCommand. Nothing here decides a state.
func runSyncStatusForStory(cmd *cobra.Command, serverArg, storyID string) error {
	cfg, root, dataDir, err := loadProcessRepoConfig()
	if err != nil {
		return err
	}
	synced, err := config.SubstrateAreas(cfg)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	var touched map[string][]string
	if len(synced) > 0 {
		paths, err := storyTouchedPaths(cmd.Context(), storyID)
		if err != nil {
			return err
		}
		touched = map[string][]string{}
		for _, p := range paths {
			if area := config.AreaOfRepoPath(cfg, root, p); area != "" && slices.Contains(synced, area) {
				touched[area] = append(touched[area], p)
			}
		}
	}
	if len(touched) == 0 {
		fmt.Fprintf(out, "story %s touched no synced substrate — nothing to check\n", storyID)
		return nil
	}
	var areas []string
	for _, a := range synced {
		if len(touched[a]) > 0 {
			areas = append(areas, a)
			slices.Sort(touched[a])
			touched[a] = slices.Compact(touched[a])
		}
	}

	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(cfg, root)
	if err != nil {
		return err
	}
	client := newHostedClient(cmd.Context(), server, root)
	statuses := probeSubstrate(cmd.Context(), cfg, root, dataDir, server, project, client, areas)
	recordSubstrateStates(root, statuses)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AREA\tSTATE\tLOCAL BASE\tHOSTED\tNEXT\tTOUCHED")
	var refused []string
	for _, st := range statuses {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", st.Area, st.State, versionLabel(st.Base), versionLabel(st.Effective), nextCommand(st), strings.Join(touched[st.Area], ", "))
		if st.State == snapsync.StateInStep || st.State == snapsync.StateBehind {
			continue
		}
		reason := st.Area + " " + st.State
		if st.State == snapsync.StateUnavailable && st.Err != nil {
			reason += " (" + st.Err.Error() + ")"
		}
		refused = append(refused, fmt.Sprintf("%s — touched %s — run %s", reason, strings.Join(touched[st.Area], ", "), nextCommand(st)))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(refused) > 0 {
		return fmt.Errorf("story %s changed substrate that is not on the hosted copy of project %q on %s:\n  %s",
			storyID, project, server, strings.Join(refused, "\n  "))
	}
	return nil
}

func runSyncStatus(cmd *cobra.Command, serverArg string) error {
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	areas, err := config.SubstrateAreas(cfg)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(areas) == 0 {
		fmt.Fprintln(out, "Nothing to check — every config area and documents is local. Set [sync] <area> = personal (or [sync] all = personal) to opt in.")
		return nil
	}
	server := resolveServer(serverArg)
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

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AREA\tSTATE\tLOCAL BASE\tHOSTED\tNEXT")
	notInStep := 0
	for _, st := range statuses {
		if st.State != snapsync.StateInStep {
			notInStep++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", st.Area, st.State, versionLabel(st.Base), versionLabel(st.Effective), nextCommand(st))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if notInStep > 0 {
		return fmt.Errorf("%d of %d area(s) not in step with project %q on %s", notInStep, len(statuses), project, server)
	}
	return nil
}

func versionLabel(v int) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("snapshot %d", v)
}

// nextCommand is the command that moves an area on from its state.
func nextCommand(st snapsync.AreaStatus) string {
	switch st.State {
	case snapsync.StateInStep:
		return "-"
	case snapsync.StateUnpushed, snapsync.StateNeverSynced:
		return "satelle sync push"
	case snapsync.StateBehind:
		return "satelle sync pull"
	case snapsync.StateDiverged:
		return "satelle sync reconcile, then satelle sync push"
	case snapsync.StateUnmerged:
		return "satelle sync reconcile (or merge " + strings.Join(st.Unmerged, ", ") + " and delete its conflict copy), then satelle sync push"
	}
	if errors.Is(st.Err, hosted.ErrLoginRequired) {
		return "satelle login"
	}
	return "check the server, then satelle sync status"
}

// substrateDrivers builds the driver for an area: documents ride the documents
// route, every config area the config route, and both keep their snapshot
// records on the config route.
type substrateDrivers struct {
	cfg                           config.Config
	repoRoot, dataDir             string
	server, project               string
	client                        *hosted.Client
	manifest                      []hosted.ConfigItem
	ign                           config.Ignore // the .satelleignore, loaded once with the drivers
	configDriver, documentsDriver *snapsync.Driver
}

// newSubstrateDrivers fetches the hosted manifest the snapshot records ride and
// builds the per-area drivers over it. It is the one place that does, so a probe,
// a reconcile and a pull cannot disagree on how an area's driver is made.
func newSubstrateDrivers(ctx context.Context, cfg config.Config, repoRoot, dataDir, server, project string, client *hosted.Client) (*substrateDrivers, error) {
	manifest, err := client.ConfigManifest(ctx, project)
	if err != nil {
		return nil, err
	}
	ign, err := config.LoadIgnore(dataDir)
	if err != nil {
		return nil, err
	}
	return &substrateDrivers{cfg: cfg, repoRoot: repoRoot, dataDir: dataDir, server: server, project: project, client: client, manifest: manifest, ign: ign}, nil
}

func (s *substrateDrivers) forArea(area string) *snapsync.Driver {
	records := newConfigRecords(s.client, s.project, s.manifest)
	if area == "documents" {
		if s.documentsDriver == nil {
			s.documentsDriver = newSnapDriver(s.cfg, s.client, nil, s.server, s.project, s.repoRoot, s.dataDir, s.ign,
				records, &docBlobs{client: s.client, project: s.project})
		}
		return s.documentsDriver
	}
	if s.configDriver == nil {
		s.configDriver = newSnapDriver(s.cfg, s.client, nil, s.server, s.project, s.repoRoot, s.dataDir, s.ign,
			records, &configBlobs{client: s.client, project: s.project, manifest: s.manifest})
	}
	return s.configDriver
}

// probeSubstrate asks the hosted copy where each area stands. A server that
// cannot be read, or a missing login, makes every area unavailable rather than
// failing the caller: engagement and doctor must not break on an offline machine.
func probeSubstrate(ctx context.Context, cfg config.Config, repoRoot, dataDir, server, project string, client *hosted.Client, areas []string) []snapsync.AreaStatus {
	drivers, err := newSubstrateDrivers(ctx, cfg, repoRoot, dataDir, server, project, client)
	if err != nil {
		out := make([]snapsync.AreaStatus, 0, len(areas))
		for _, a := range areas {
			out = append(out, snapsync.AreaStatus{Area: a, State: snapsync.StateUnavailable, Err: err})
		}
		return out
	}
	out := make([]snapsync.AreaStatus, 0, len(areas))
	for _, a := range areas {
		out = append(out, areaStatus(ctx, cfg, repoRoot, drivers.forArea(a), a))
	}
	return out
}

// areaStatus is snapsync's Status with any failure folded into an unavailable
// answer, so one unreadable area never hides the others.
func areaStatus(ctx context.Context, cfg config.Config, repoRoot string, d *snapsync.Driver, area string) snapsync.AreaStatus {
	local, err := snapsync.LocalShas(cfg, repoRoot, area, d.Ignore)
	if err != nil {
		return snapsync.AreaStatus{Area: area, State: snapsync.StateUnavailable, Err: fmt.Errorf("read local files: %w", err)}
	}
	st, err := d.Status(ctx, area, local)
	if err != nil {
		return snapsync.AreaStatus{Area: area, State: snapsync.StateUnavailable, Err: err}
	}
	return st
}

// recordSubstrateStates records the verified states and the time they were
// verified. An unavailable answer verified nothing, so it is not recorded: an
// offline machine must not look freshly checked. Recording is best effort — a
// state file that cannot be written never fails the command that asked.
func recordSubstrateStates(repoRoot string, statuses []snapsync.AreaStatus) {
	states := map[string]string{}
	for _, st := range statuses {
		if st.State != snapsync.StateUnavailable {
			states[st.Area] = st.State
		}
	}
	_ = syncstate.RecordSubstrate(config.GlobalDir(), repoRoot, states, time.Now())
}

// recordAreaStates is the record an operation leaves behind: after a push, pull,
// deploy or rehydrate it asks the driver where each handled area now stands —
// never assuming the operation left it in step. A pull that left a conflict or an
// incomplete entry therefore records unmerged or behind.
func recordAreaStates(ctx context.Context, cfg config.Config, repoRoot string, d *snapsync.Driver, areas []string) {
	statuses := make([]snapsync.AreaStatus, 0, len(areas))
	for _, a := range areas {
		statuses = append(statuses, areaStatus(ctx, cfg, repoRoot, d, a))
	}
	recordSubstrateStates(repoRoot, statuses)
}
