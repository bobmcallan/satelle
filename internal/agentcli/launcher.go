package agentcli

import (
	"sort"
	"strings"
)

// launcherBinaries maps a harness that has a satelle launcher (`satelle agents
// install`) to the binary the launcher execs. It is the ONE owner of "this
// harness has a launcher": agentinstall.Content switches on it, and the install
// note and BindingSnippet read it, so none can disagree.
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
