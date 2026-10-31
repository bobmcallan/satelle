package cli

// Substrate continuity advisories (sty_7562055c): one line, printed where a
// developer is about to work, when the authored substrate or documents on this
// machine are not in step with the bound project's hosted copy. Advisory only —
// nothing here refuses an engagement or fails a session, and a machine that is
// in step prints nothing.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// substrateProbeTimeout bounds the live check an engagement makes. It is a
// mechanism limit, not a process choice: an engagement must never wait on a slow
// server.
const substrateProbeTimeout = 3 * time.Second

// substrateRefreshDebounce is the least gap between two detached refresh starts,
// so a server that is down does not cost a spawn on every session start.
const substrateRefreshDebounce = 10 * time.Minute

const (
	substrateUnverified   = "unverified (offline/unbound)"
	substrateSyncStatus   = "run `satelle sync status`"
	substrateDefaultCmd   = substrateSyncStatus + " for the next command"
	substrateUncheckedFmt = "⚠️ satelle: substrate not checked against its hosted copy — %s unverified; %s."
)

// substrateAdvisoryFor is the seam runHookContext reads the session-start line
// through; tests replace it.
var substrateAdvisoryFor = substrateAdvisory

// substrateRefresh is the spawner the session-start gatherer starts. Under
// `go test`, os.Executable is the test binary, so a real spawn would run a
// detached copy of the suite. TestMain replaces it with a no-op; tests of the
// spawn decision inject their own spawner.
var substrateRefresh = spawnSubstrateRefresh

// substrateNotice is what one substrate advisory line says.
type substrateNotice struct {
	drifted     []string // "area state" for each area not in step
	unchecked   []string // areas the doctor reports unchecked (never, or too long ago)
	command     string   // what brings the drifted areas in step; "" → the default
	probeFailed bool     // a live check was tried and failed: the line is from recorded state
}

// substrateLine renders the one advisory line; "" when there is nothing to say.
// It is the only formatter: drifted areas get the drift line, and areas that are
// only unchecked get their own.
func substrateLine(n substrateNotice) string {
	if len(n.drifted) == 0 {
		if len(n.unchecked) == 0 || n.probeFailed {
			return ""
		}
		return fmt.Sprintf(substrateUncheckedFmt, strings.Join(n.unchecked, ", "), substrateSyncStatus)
	}
	tail := ""
	switch {
	case n.probeFailed:
		tail = " — " + substrateUnverified
	case len(n.unchecked) > 0:
		tail = fmt.Sprintf(" (unverified: %s not checked recently)", strings.Join(n.unchecked, ", "))
	}
	cmd := n.command
	if cmd == "" {
		cmd = substrateDefaultCmd
	}
	return fmt.Sprintf("⚠️ satelle: substrate not in step with its hosted copy — %s%s; %s.",
		strings.Join(n.drifted, ", "), tail, cmd)
}

// substrateNoticeFrom reads the doctor's offline judgement. The doctor owns what
// counts as drifted and as unchecked (including [sync] stale_after); this only
// sorts its findings. unbound reports a machine with no signed-in server and
// bound project, which a refresh cannot help.
func substrateNoticeFrom(findings health.Findings) (n substrateNotice, unbound bool) {
	var remedies []string
	for _, f := range findings {
		switch f.ID {
		case health.IDSyncSubstrateUnpushed, health.IDSyncSubstrateUnmerged, health.IDSyncSubstrateBehind:
			if f.Severity != health.SeverityError {
				continue
			}
			n.drifted = append(n.drifted, f.Artifact+" "+strings.TrimPrefix(f.Title, "Substrate "))
			if f.Remediation != "" && !slices.Contains(remedies, f.Remediation) {
				remedies = append(remedies, f.Remediation)
			}
		case health.IDSyncSubstrateUnchecked:
			if !slices.Contains(n.unchecked, f.Artifact) {
				n.unchecked = append(n.unchecked, f.Artifact)
			}
		case health.IDSyncSubstrateUnbound:
			unbound = true
		}
	}
	if len(remedies) == 1 {
		n.command = remedies[0]
	}
	return n, unbound
}

// substrateAdvisoryAt is the session-start decision over explicit inputs: the
// line, and whether a detached refresh should start — some area is unchecked,
// the machine can reach a server at all, and none began within the debounce.
func substrateAdvisoryAt(findings health.Findings, lastAttempt, now time.Time) (line string, refresh bool) {
	n, unbound := substrateNoticeFrom(findings)
	refresh = len(n.unchecked) > 0 && !unbound &&
		(lastAttempt.IsZero() || now.Sub(lastAttempt) >= substrateRefreshDebounce)
	return substrateLine(n), refresh
}

// substrateAdvisory is the session-start line, from local and recorded state
// alone: SessionStart never waits on the server. When areas are unchecked it
// starts a detached `satelle sync status` (through substrateRefresh, never
// waited on) that records their state for the next session; its only write is
// the attempt stamp.
func substrateAdvisory(a *app.App) string {
	globalDir, now := config.GlobalDir(), time.Now()
	findings := doctor.CheckSubstrateSync(a.RepoRoot, a.DataDir, globalDir, now)
	st, _, _ := syncstate.Load(globalDir, a.RepoRoot)
	line, refresh := substrateAdvisoryAt(findings, st.SubstrateCheckedAttempt, now)
	// Without the stamp the debounce cannot hold, so no stamp means no spawn.
	if refresh && syncstate.MarkSubstrateAttempt(globalDir, a.RepoRoot, now) == nil {
		substrateRefresh(a.RepoRoot)
	}
	return line
}

// spawnSubstrateRefresh starts `satelle sync status` detached from the hook: its
// own session, no inherited stdio, and no Wait. Every failure is swallowed.
func spawnSubstrateRefresh(repoRoot string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "sync", "status")
	cmd.Dir = repoRoot
	detachChild(cmd)
	if cmd.Start() != nil {
		return
	}
	_ = cmd.Process.Release()
}

// substrateAdvisoryOffline is the engagement fallback when the live check could
// not be made: the doctor's offline judgement from local and recorded state,
// which never contacts the server. It says so — the record is only as fresh as
// the last check.
func substrateAdvisoryOffline(repoRoot, dataDir string) string {
	n, _ := substrateNoticeFrom(doctor.CheckSubstrateSync(repoRoot, dataDir, config.GlobalDir(), time.Now()))
	n.probeFailed = true
	return substrateLine(n)
}

// substrateAdvisoryLive checks the hosted copy within substrateProbeTimeout,
// records what it found, and renders the advisory from it. The areas it checks
// are those of the scope the last sync resolved for the explicit binding
// (syncstate.RecordedScope), the same scope the doctor judges from. A server
// that cannot be reached, or an unbound machine, falls back to the offline line
// marked unverified.
func substrateAdvisoryLive(ctx context.Context, cfg config.Config, repoRoot, dataDir string) string {
	project := cfg.SyncProject()
	areas := config.SubstrateAreas(syncstate.RecordedScope(config.GlobalDir(), repoRoot, project))
	if len(areas) == 0 {
		return ""
	}
	server := resolveServer("")
	if project == "" || server == "" {
		return substrateAdvisoryOffline(repoRoot, dataDir)
	}
	if _, serr := hosted.DefaultStore().Load(server); serr != nil {
		return substrateAdvisoryOffline(repoRoot, dataDir)
	}
	ctx, cancel := context.WithTimeout(ctx, substrateProbeTimeout)
	defer cancel()
	client := newHostedClient(ctx, server, repoRoot)
	statuses := probeSubstrate(ctx, cfg, repoRoot, dataDir, server, project, client, areas)
	var drifted []string
	verified := false
	for _, st := range statuses {
		if st.State == snapsync.StateUnavailable {
			continue
		}
		verified = true
		if st.State != snapsync.StateInStep {
			drifted = append(drifted, st.Area+" "+st.State)
		}
	}
	if !verified {
		return substrateAdvisoryOffline(repoRoot, dataDir)
	}
	recordSubstrateStates(repoRoot, statuses)
	return substrateLine(substrateNotice{drifted: drifted})
}

// substrateEngagementAdvisory runs after `story set --status`: when the story is
// now in a performing state of its workflow — an engagement — it checks the
// substrate and prints the advisory on stderr. Engagement is never refused and a
// failure here is silent: the transition already committed.
func substrateEngagementAdvisory(cmd *cobra.Command, id string) {
	a, err := appFrom(cmd)
	if err != nil || a == nil {
		return
	}
	ctx := cmd.Context()
	it, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		return
	}
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return
	}
	spec, _, _, serr := wfgovern.SpecFor(wfs, it)
	if serr != nil || !slices.Contains(spec.NonTerminalEngagingStates(), it.Status) {
		return
	}
	if line := substrateAdvisoryLive(ctx, a.Config, a.RepoRoot, a.DataDir); line != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
}
