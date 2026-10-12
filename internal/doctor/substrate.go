package doctor

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// signedIn reports whether this machine holds a credential for the server. A
// variable so tests can stand in for the machine's credential store.
var signedIn = func(server string) bool {
	_, err := hosted.DefaultStore().Load(server)
	return err == nil
}

// CheckSubstrateSync reports whether the authored substrate and documents are in
// step with their hosted copy, from local state alone: each area's files against
// the base it last synced, that base's unmerged paths, and the last state a
// command verified against the server. It never contacts the server
// (sty_7562055c); a recorded check is how a past answer from the server reaches
// it.
func CheckSubstrateSync(repoRoot, dataDir, globalDir string, now time.Time) health.Findings {
	cfgPath := filepath.Join(dataDir, config.ConfigName)
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		return health.Findings{health.Warn(health.IDSyncConfig, "Substrate sync config",
			"could not load satelle.toml; substrate sync not judged").About(cfgPath)}
	}
	areas, err := config.SubstrateAreas(cfg)
	if err != nil {
		return health.Findings{health.Error(health.IDSyncConfig, "Substrate sync config", err.Error()).About(cfgPath)}
	}
	if len(areas) == 0 {
		return health.Findings{health.Info(health.IDSyncSubstrateOk, "Substrate is local-only",
			"every substrate area is local; not flagged as unbacked").About(repoRoot)}
	}

	server := config.ResolveHostedServer(cfg)
	project := config.ResolveBoundProject(cfg, repoRoot)
	var out health.Findings
	if project == "" || !signedIn(server) {
		out = append(out, health.Error(health.IDSyncSubstrateUnbound, "Substrate unbound",
			fmt.Sprintf("%s: %s are opted in to sync but no signed-in server and bound project hold their hosted copy", repoRoot, strings.Join(areas, ", "))).
			About(repoRoot).WithRemediation("run `satelle login`, then `satelle project bind <slug>`"))
		if project == "" {
			return out // nothing keys this checkout's synced bases
		}
	}
	abs, aerr := filepath.Abs(repoRoot)
	if aerr != nil {
		abs = repoRoot
	}

	threshold, judged, terr := config.WorkstateStaleAfter(cfg)
	if terr != nil {
		return append(out, health.Error(health.IDSyncConfig, "Substrate sync config", terr.Error()).About(cfgPath))
	}
	if !judged {
		out = append(out, health.Warn(health.IDSyncConfig, "Substrate staleness not judged",
			"no [sync] stale_after; run `satelle init` or `satelle migrate --yes` to seed it").About(cfgPath))
	}
	st, _, lerr := syncstate.Load(globalDir, repoRoot)
	if lerr != nil {
		return append(out, health.Warn(health.IDSyncConfig, "Substrate sync state", lerr.Error()).About(repoRoot))
	}

	bad := 0
	for _, area := range areas {
		found := judgeSubstrateArea(cfg, repoRoot, dataDir, abs, server, project, area, st, threshold, judged, now)
		bad += len(found)
		out = append(out, found...)
	}
	if bad == 0 {
		out = append(out, health.Info(health.IDSyncSubstrateOk, "Substrate in step",
			fmt.Sprintf("%s match their last synced snapshot and the last hosted check", strings.Join(areas, ", "))).About(repoRoot))
	}
	return out
}

func judgeSubstrateArea(cfg config.Config, repoRoot, dataDir, abs, server, project, area string, st syncstate.State, threshold time.Duration, judged bool, now time.Time) health.Findings {
	title := "Substrate " + area
	local, err := snapsync.LocalShas(cfg, repoRoot, area)
	if err != nil {
		return health.Findings{health.Warn(health.IDSyncConfig, title,
			fmt.Sprintf("%s: cannot read the local files (%v); not judged", area, err)).About(repoRoot)}
	}
	drift, err := snapsync.OfflineDrift(server, project, abs, dataDir, area, local)
	if err != nil {
		return health.Findings{health.Warn(health.IDSyncConfig, title,
			fmt.Sprintf("%s: cannot read the synced base (%v); not judged", area, err)).About(repoRoot)}
	}

	var out health.Findings
	if len(drift.Unmerged) > 0 {
		out = append(out, health.Error(health.IDSyncSubstrateUnmerged, "Substrate "+snapsync.StateUnmerged,
			fmt.Sprintf("%s: a pull left %s unmerged", area, pathList(drift.Unmerged))).
			About(area).WithRemediation(fmt.Sprintf("run `satelle sync reconcile` and choose a side per change (or merge it and delete %s), then run `satelle sync`", filepath.Join(dataDir, filepath.FromSlash(snapsync.CopyPath(area, drift.Unmerged[0]))))))
	}
	if len(drift.Changed) > 0 {
		since, state := "the last synced snapshot", snapsync.StateUnpushed
		if !drift.Synced {
			since, state = "the hosted copy (never synced)", snapsync.StateNeverSynced
		}
		out = append(out, health.Error(health.IDSyncSubstrateUnpushed, "Substrate "+state,
			fmt.Sprintf("%s: %s differ from %s", area, pathList(drift.Changed), since)).
			About(area).WithRemediation("run `satelle sync`"))
	}

	rec, checked := st.SubstrateAreas[area]
	switch {
	case checked && (rec.State == snapsync.StateBehind || rec.State == snapsync.StateDiverged):
		out = append(out, health.Error(health.IDSyncSubstrateBehind, "Substrate "+rec.State,
			fmt.Sprintf("%s: the last hosted check (%s) found it %s", area, rec.CheckedAt.UTC().Format(time.RFC3339), rec.State)).
			About(area).WithRemediation(substrateRemediation(area, rec.State)))
	case !judged:
	case !checked:
		out = append(out, health.Error(health.IDSyncSubstrateUnchecked, "Substrate unchecked",
			fmt.Sprintf("%s: never checked against the hosted copy (threshold %s)", area, threshold)).
			About(area).WithRemediation("run `satelle sync status`"))
	case now.Sub(rec.CheckedAt) > threshold:
		out = append(out, health.Error(health.IDSyncSubstrateUnchecked, "Substrate unchecked",
			fmt.Sprintf("%s: last checked against the hosted copy %s ago (threshold %s)", area, formatAge(now.Sub(rec.CheckedAt)), threshold)).
			About(area).WithRemediation("run `satelle sync status`"))
	}
	return out
}

// substrateRemediation is the command that brings an area back in step from the
// given state.
func substrateRemediation(area, state string) string {
	pull := "`satelle sync config deploy`"
	if area == "documents" {
		pull = "`satelle sync documents pull`"
	}
	if state == snapsync.StateDiverged {
		return "run `satelle sync reconcile` and choose a side per change, then `satelle sync`"
	}
	return "run " + pull
}

func pathList(paths []string) string {
	const max = 3
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:max], ", "), len(paths)-max)
}
