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

// launcherNameFlags maps a harness to every spelling of its launch-time
// session-name flag. It is the ONE owner of "this harness can be named at
// launch": agentinstall generates the launcher's guard from it (so a user-passed
// name is never overridden), and `satelle session name` and init's hint read it.
// A harness with no entry has no such flag and records an explicit unavailable.
//
// claude: `-n, --name <name>` ("Set a display name for this session", claude
// --help), which sets the peer name ListAgents shows. pi: `--name, -n <name>`
// ("Set session display name", pi --help). grok and cursor show none in their
// --help: no session-name flag is recorded for them; add one here when a
// harness's --help shows it. A flag here does not give a harness a launcher
// (see launcherBinaries): pi is named by `satelle session name`, not wrapped.
// Each long flag also matches its `--flag=value` spelling in the launcher guard.
var launcherNameFlags = map[string][]string{
	HarnessClaude: {"--name", "-n"},
	HarnessPi:     {"--name", "-n"},
}

// LaunchNameFlags lists the spellings of harness's launch name flag, or nil when
// it has none. The first spelling is the one a launcher injects.
func LaunchNameFlags(harness string) []string {
	return append([]string(nil), launcherNameFlags[strings.ToLower(strings.TrimSpace(harness))]...)
}

// NameableHarnesses lists the harnesses that declare a launch name flag and have
// a launcher, sorted.
func NameableHarnesses() []string {
	var out []string
	for _, h := range LauncherHarnesses() {
		if len(launcherNameFlags[h]) > 0 {
			out = append(out, h)
		}
	}
	return out
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
