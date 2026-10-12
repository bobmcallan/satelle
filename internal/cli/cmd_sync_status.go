package cli

// Substrate continuity (sty_7562055c): where each authored area and the documents
// stand against the bound project's hosted copy. Everything that DECIDES a state
// lives in snapsync.Driver.Status, which asks the same push-planning path a push
// runs; this file builds the drivers, records what they answer and prints it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

func newSyncStatusCmd() *cobra.Command {
	var server string
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
without the server.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncStatus(cmd, server)
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	return cmd
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
	pull := "satelle sync config deploy"
	if st.Area == "documents" {
		pull = "satelle sync documents pull"
	}
	switch st.State {
	case snapsync.StateInStep:
		return "-"
	case snapsync.StateUnpushed, snapsync.StateNeverSynced:
		return "satelle sync"
	case snapsync.StateBehind:
		return pull
	case snapsync.StateDiverged:
		return "satelle sync reconcile, then satelle sync"
	case snapsync.StateUnmerged:
		return "satelle sync reconcile (or merge " + strings.Join(st.Unmerged, ", ") + " and delete its conflict copy), then satelle sync"
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
	configDriver, documentsDriver *snapsync.Driver
}

func (s *substrateDrivers) forArea(area string) *snapsync.Driver {
	records := newConfigRecords(s.client, s.project, s.manifest)
	if area == "documents" {
		if s.documentsDriver == nil {
			s.documentsDriver = newSnapDriver(s.cfg, s.client, nil, s.server, s.project, s.repoRoot, s.dataDir,
				"satelle sync documents pull", records, &docBlobs{client: s.client, project: s.project})
		}
		return s.documentsDriver
	}
	if s.configDriver == nil {
		s.configDriver = newSnapDriver(s.cfg, s.client, nil, s.server, s.project, s.repoRoot, s.dataDir,
			"satelle sync rehydrate", records, &configBlobs{client: s.client, project: s.project, manifest: s.manifest})
	}
	return s.configDriver
}

// probeSubstrate asks the hosted copy where each area stands. A server that
// cannot be read, or a missing login, makes every area unavailable rather than
// failing the caller: engagement and doctor must not break on an offline machine.
func probeSubstrate(ctx context.Context, cfg config.Config, repoRoot, dataDir, server, project string, client *hosted.Client, areas []string) []snapsync.AreaStatus {
	manifest, err := client.ConfigManifest(ctx, project)
	if err != nil {
		out := make([]snapsync.AreaStatus, 0, len(areas))
		for _, a := range areas {
			out = append(out, snapsync.AreaStatus{Area: a, State: snapsync.StateUnavailable, Err: err})
		}
		return out
	}
	drivers := &substrateDrivers{cfg: cfg, repoRoot: repoRoot, dataDir: dataDir, server: server, project: project, client: client, manifest: manifest}
	out := make([]snapsync.AreaStatus, 0, len(areas))
	for _, a := range areas {
		out = append(out, areaStatus(ctx, cfg, repoRoot, drivers.forArea(a), a))
	}
	return out
}

// areaStatus is snapsync's Status with any failure folded into an unavailable
// answer, so one unreadable area never hides the others.
func areaStatus(ctx context.Context, cfg config.Config, repoRoot string, d *snapsync.Driver, area string) snapsync.AreaStatus {
	local, err := snapsync.LocalShas(cfg, repoRoot, area)
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
