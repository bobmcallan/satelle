// Session-start release currency (sty_15a374a8): one line, from the release
// cache `satelle update [--check]` records, naming a newer satelle or satelled
// release. SessionStart never waits on the network, so this reads only the cache
// and, when the cache is old, starts a detached `satelle update --check` that
// refreshes it for the next session.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/releasestate"
)

const (
	// releaseCacheTTL is how old the cache may be before the line is marked
	// unverified and a refresh is started.
	releaseCacheTTL = 24 * time.Hour
	// releaseRefreshDebounce is the least gap between two refresh starts, so a
	// network that is down does not cost a spawn on every session start.
	releaseRefreshDebounce = 10 * time.Minute
)

// releaseAdvisoryFor is the seam runHookContext reads the line through; tests
// replace it (the real gatherer is silent for the dev build a test runs as).
var releaseAdvisoryFor = releaseAdvisory

// releaseAdvisory gathers the inputs releaseCurrencyLine decides on. In a pinned
// repo the hook already runs AS the pin (Execute re-execs into it), so the build
// version here is the pin's. A dev build neither advises nor refreshes. It has
// NO error channel by construction: it feeds `satelle hook context`, which must
// always exit 0.
func releaseAdvisory(a *app.App) string {
	running := strings.TrimSpace(buildinfo.Resolve().Version)
	if isDevVersion(running) {
		return ""
	}
	_, pinned := repoPin(a.RepoRoot)
	return releaseAdvisoryAt(config.GlobalDir(), running, pinned, time.Now(), spawnReleaseRefresh)
}

// releaseAdvisoryAt is the gatherer over explicit inputs: read the cache, start
// the detached refresh (through spawn, never waited on) when the cache is
// missing or stale and no refresh began in the last debounce window, and decide
// the line. Its only write is the checked_attempt stamp.
func releaseAdvisoryAt(globalDir, runningCLI string, pinned bool, now time.Time, spawn func()) string {
	st, _, _ := releasestate.Load(globalDir)
	// A file holding only a checked_attempt stamp is a cache that was never filled.
	missing := st.CheckedAt.IsZero()
	stale := !missing && now.Sub(st.CheckedAt) > releaseCacheTTL
	if (missing || stale) && (st.CheckedAttempt.IsZero() || now.Sub(st.CheckedAttempt) >= releaseRefreshDebounce) {
		// Without the stamp the debounce cannot hold, so no stamp means no spawn.
		if releasestate.MarkAttempt(globalDir, now) == nil {
			spawn()
		}
	}
	if missing {
		return ""
	}
	return releaseCurrencyLine(runningCLI, st.ServeRunning, pinned, st, now)
}

// releaseCurrencyLine is the DECISION: one advisory line when the cached latest
// CLI or serve release is newer, by ordered version comparison, than the running
// one, "" otherwise. Running AHEAD of latest (a build newer than the published
// release) is silent, as is a dev build or a version that does not parse.
func releaseCurrencyLine(runningCLI, runningServe string, pinned bool, st releasestate.State, now time.Time) string {
	if isDevVersion(runningCLI) {
		return ""
	}
	var parts []string
	if cli := outdated(runningCLI, st.CLILatest); cli != "" {
		parts = append(parts, "satelle "+cli)
	}
	if serve := outdated(runningServe, st.ServeLatest); serve != "" {
		parts = append(parts, "satelled "+serve)
	}
	if len(parts) == 0 {
		return ""
	}
	line := "⚠️ satelle: a newer release exists — " + strings.Join(parts, ", ")
	if !st.CheckedAt.IsZero() && now.Sub(st.CheckedAt) > releaseCacheTTL {
		line += fmt.Sprintf(" (unverified (last checked %s))", st.CheckedAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	line += " — run `satelle update`"
	if pinned {
		line += "; this repo pins its own binary, so also run `satelle update --local`"
	}
	return line + "."
}

// outdated returns "running → latest" when latest is strictly newer than running,
// else "". Either side that is empty or does not parse as MAJOR.MINOR.PATCH
// yields "": an advisory is never made from a version it cannot order.
func outdated(running, latest string) string {
	run, ok := parseTagVersion(tagVersion(running))
	if !ok {
		return ""
	}
	lat, ok := parseTagVersion(tagVersion(latest))
	if !ok || !lessVersion(run, lat) {
		return ""
	}
	return tagVersion(running) + " → " + tagVersion(latest)
}

// spawnReleaseRefresh starts `satelle update --check` detached from this hook:
// its own session, no inherited stdio (an inherited pipe would hold the harness
// open), and no Wait. Every failure is swallowed — the refresh is best effort.
func spawnReleaseRefresh() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "update", "--check")
	detachChild(cmd)
	if cmd.Start() != nil {
		return
	}
	_ = cmd.Process.Release()
}
