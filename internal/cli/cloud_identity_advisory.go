package cli

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// cloudIdentityAdvisoryFor is the seam runHookContext reads the line through;
// tests replace it, as with the other session-start advisories.
var cloudIdentityAdvisoryFor = cloudIdentityAdvisory

// cloudIdentityAdvisory is the session-start line for a repo whose in-loop agent
// profile names a cloud identity (sty_b61d2066). It reads LOCAL state only — the
// repo's agents layer, the catalog file and the environment passed in — runs no
// subprocess and makes no network call, so it adds no latency.
//
//   - a session started through a launcher (LauncherEnv set): nothing to say;
//   - a harness with a launcher, started without it: the session is not running as
//     the named identity, so say so;
//   - a harness with no launcher (pi, cursor): the identity is unavailable there,
//     named under the adapter, rather than a warning about a launcher that does
//     not exist.
//
// An unresolved credential reference is not this line's business (the launcher
// refuses and `satelle agent validate` reports it); a repo that names an identity
// is warned either way.
func cloudIdentityAdvisory(a *app.App, harness string, environ []string) string {
	if a == nil {
		return ""
	}
	root := a.ProcessRoot
	if strings.TrimSpace(root) == "" {
		root = a.RepoRoot
	}
	sel, ok, _ := cloudSelectionAt(a.PlaneDir(), root)
	if !ok {
		return ""
	}
	return cloudIdentityLine(sel.Profile, harness, environ)
}

// cloudIdentityLine is the pure decision of cloudIdentityAdvisory.
func cloudIdentityLine(profile, harness string, environ []string) string {
	if harness == "" {
		harness, _ = agentcli.InLoopHarnessFromEnv(environ)
	}
	if harness == "" {
		return ""
	}
	if reason := agentcli.CloudIdentityUnavailable(harness); reason != "" {
		return fmt.Sprintf("cloud identity: unavailable (%s) — this repo's agent profile %q names one, so cloud commands here run as your own identity", reason, profile)
	}
	for _, kv := range environ {
		if v, found := strings.CutPrefix(kv, config.LauncherEnv+"="); found && strings.TrimSpace(v) != "" {
			return ""
		}
	}
	return fmt.Sprintf("cloud identity: this repo's agent profile %q names a cloud identity, but this session was not started through `satelle-%s` (installed by `satelle agents install %s`) — cloud commands run as your own identity",
		profile, harness, harness)
}
