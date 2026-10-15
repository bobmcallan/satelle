package agentcli

import (
	"fmt"
	"sort"
	"strings"
)

// launcherBinaries maps a harness that has a satelle launcher (`satelle agents
// install`, sty_b61d2066) to the binary the launcher finally execs. It is the
// ONE owner of "this harness has a launcher": agentinstall.Content switches on
// it, and the install note, the `agents launch` refusal, the session-start
// warning and the capability-table cell all read it, so none can disagree.
var launcherBinaries = map[string]string{
	HarnessClaude: CLIClaude,
	HarnessGrok:   CLIGrok,
}

// HasLauncher reports whether harness has a satelle launcher.
func HasLauncher(harness string) bool {
	_, ok := launcherBinaries[strings.ToLower(strings.TrimSpace(harness))]
	return ok
}

// LauncherBinary is the binary harness's launcher execs, or "" when it has no
// launcher.
func LauncherBinary(harness string) string {
	return launcherBinaries[strings.ToLower(strings.TrimSpace(harness))]
}

// LauncherHarnesses lists the harnesses that have a launcher, sorted.
func LauncherHarnesses() []string {
	out := make([]string, 0, len(launcherBinaries))
	for h := range launcherBinaries {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// LaunchStepsUnavailable is the adapter-named reason a harness cannot run a
// profile's launch steps: it has no launcher to run them through. Empty when it
// has one.
func LaunchStepsUnavailable(harness string) string {
	if HasLauncher(harness) {
		return ""
	}
	return fmt.Sprintf("%s has no satelle launcher", strings.TrimSpace(harness))
}

// launchStepsCapability is the CapabilityTable launch-steps cell for an adapter
// row ("<harness> <transport>"): available when its harness has a launcher, else
// unavailable with the adapter-named reason.
func launchStepsCapability(adapter string) Capability {
	harness, _, _ := strings.Cut(adapter, " ")
	if reason := LaunchStepsUnavailable(harness); reason != "" {
		return no(reason)
	}
	return yes()
}

// ComposeLaunchEnv is the environment of a launched harness: base with every
// unset name removed, then overlay layered on it through composeEnv (overlay
// wins). It is the same composition every other child gets; the unsets are not
// a secret filter — they are whatever the profile's launch table declares.
func ComposeLaunchEnv(base []string, overlay map[string]string, unset []string) []string {
	if len(unset) > 0 {
		drop := make(map[string]bool, len(unset))
		for _, k := range unset {
			drop[k] = true
		}
		kept := make([]string, 0, len(base))
		for _, kv := range base {
			key := kv
			if i := strings.IndexByte(kv, '='); i >= 0 {
				key = kv[:i]
			}
			if !drop[key] {
				kept = append(kept, kv)
			}
		}
		base = kept
	}
	return composeEnv(base, overlay)
}
