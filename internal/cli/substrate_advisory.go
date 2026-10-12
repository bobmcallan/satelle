package cli

// Substrate continuity advisories (sty_7562055c): one line, printed where a
// developer is about to work, when the authored substrate or documents on this
// machine are not in step with the bound project's hosted copy. Advisory only —
// nothing here refuses an engagement or fails a session, and a machine that is
// in step prints nothing.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// substrateProbeTimeout bounds the live check an engagement makes. It is a
// mechanism limit, not a process choice: an engagement must never wait on a slow
// server.
const substrateProbeTimeout = 3 * time.Second

const substrateUnverified = "unverified (offline/unbound)"

// substrateLine renders the one advisory line from the areas that are not in
// step as "area state" pairs; "" when there are none.
func substrateLine(drifted []string, unverified bool) string {
	if len(drifted) == 0 {
		return ""
	}
	tail := ""
	if unverified {
		tail = " — " + substrateUnverified
	}
	return fmt.Sprintf("⚠️ satelle: substrate not in step with its hosted copy — %s%s; run `satelle sync status` for the next command.",
		strings.Join(drifted, ", "), tail)
}

// substrateAdvisoryOffline is the advisory from local and recorded state alone —
// the doctor's offline judgement — and never contacts the server. It is
// unverified by construction: the recorded state is only as fresh as the last
// check.
func substrateAdvisoryOffline(repoRoot, dataDir string) string {
	var drifted []string
	for _, f := range doctor.CheckSubstrateSync(repoRoot, dataDir, config.GlobalDir(), time.Now()) {
		if f.Severity != health.SeverityError {
			continue
		}
		switch f.ID {
		case health.IDSyncSubstrateUnpushed, health.IDSyncSubstrateUnmerged, health.IDSyncSubstrateBehind:
			drifted = append(drifted, f.Artifact+" "+strings.TrimPrefix(f.Title, "Substrate "))
		}
	}
	return substrateLine(drifted, true)
}

// substrateAdvisoryLive checks the hosted copy within substrateProbeTimeout,
// records what it found, and renders the advisory from it. A server that cannot
// be reached, or an unbound machine, falls back to the offline line marked
// unverified.
func substrateAdvisoryLive(ctx context.Context, cfg config.Config, repoRoot, dataDir string) string {
	areas, err := config.SubstrateAreas(cfg)
	if err != nil || len(areas) == 0 {
		return ""
	}
	server := resolveServer("")
	project := config.ResolveBoundProject(cfg, repoRoot)
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
	return substrateLine(drifted, false)
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
