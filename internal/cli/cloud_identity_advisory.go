package cli

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// launchAdvisoryFor is the seam runHookContext reads the line through; tests
// replace it, as with the other session-start advisories.
var launchAdvisoryFor = launchAdvisory

// launchAdvisory is the session-start line for a repo whose in-loop agent
// profile declares launch steps (sty_b8310408). It reads LOCAL state only — the
// repo's agents layer, the catalog file and the environment passed in — runs no
// subprocess and makes no network call, so it adds no latency.
//
//   - a session started through a launcher (LauncherEnv set): nothing to say;
//   - a harness with a launcher, started without it: the declared steps did not
//     run, so say so;
//   - a harness with no launcher (pi, cursor): launch steps are unavailable
//     there, named under the adapter, rather than a warning about a launcher
//     that does not exist.
//
// An unresolved secrets reference is not this line's business (the launcher
// refuses and `satelle agent validate` reports it); a repo that declares steps is
// warned either way.
func launchAdvisory(a *app.App, harness string, environ []string) string {
	if a == nil {
		return ""
	}
	root := a.ProcessRoot
	if strings.TrimSpace(root) == "" {
		root = a.RepoRoot
	}
	sel, ok, _ := launchSelectionAt(a.PlaneDir(), root)
	if !ok {
		return ""
	}
	return launchLine(sel.Profile, harness, environ)
}

// launchLine is the pure decision of launchAdvisory.
func launchLine(profile, harness string, environ []string) string {
	if harness == "" {
		harness, _ = agentcli.InLoopHarnessFromEnv(environ)
	}
	if harness == "" {
		return ""
	}
	if reason := agentcli.LaunchStepsUnavailable(harness); reason != "" {
		return fmt.Sprintf("launch steps: unavailable (%s) — this repo's agent profile %q declares some, so they do not run in this session", reason, profile)
	}
	for _, kv := range environ {
		if v, found := strings.CutPrefix(kv, config.LauncherEnv+"="); found && strings.TrimSpace(v) != "" {
			return ""
		}
	}
	return fmt.Sprintf("launch steps: this repo's agent profile %q declares launch steps, but this session was not started through `satelle-%s` (installed by `satelle agents install %s`) — they did not run",
		profile, harness, harness)
}
