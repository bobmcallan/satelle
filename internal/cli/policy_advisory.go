package cli

// Subscribed command-policy advisories (sty_041bf275): the session-start lines
// that say whether a repo's subscribed lists are current, and the detached,
// debounced refresh that keeps them so. It is modelled on substrate_advisory.go.
// Session start reads the local cache only and never waits on a refresh; the
// refresh is `satelle policy refresh`, started detached through a seam the tests
// disable. A repo with no [[gate.command_policy]] reads no cache and spawns
// nothing.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// policyAdvisoryFor is the seam runHookContext reads the session-start lines
// through; tests replace it.
var policyAdvisoryFor = policyAdvisory

// policyRefresh is the spawner the session-start gatherer starts. Under
// `go test`, os.Executable is the test binary, so a real spawn would run a
// detached copy of the suite. TestMain replaces it with a no-op; tests of the
// spawn decision inject their own spawner.
var policyRefresh = spawnPolicyRefresh

const (
	policySourceCatalog = "catalog"
	policySourceURL     = "url"
)

// policySourceKind is where a subscription's list is fetched from.
func policySourceKind(sub config.CommandPolicy) string {
	if sub.IsCatalog() {
		return policySourceCatalog
	}
	return policySourceURL
}

// policyCached is the cache entry that belongs to sub: ok is false when nothing
// was cached for it, or what was cached came from a source the subscription no
// longer names. A failure recorded against the current source is still returned.
func policyCached(sub config.CommandPolicy, st syncstate.PolicyState) (syncstate.PolicySub, bool) {
	e, ok := st.Subs[sub.Name]
	if !ok || e.Source != sub.Source() || e.SourceKind != policySourceKind(sub) {
		return syncstate.PolicySub{}, false
	}
	return e, true
}

func policyTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// policyUnverifiedSource is said of every URL list: nothing about it can be
// checked, because the source gives no checksum or signature.
const policyUnverifiedSource = "from an unverified source (url, no checksum)"

// policyLine is the one session-start line for a subscription; "" when its list
// is present, current and checkable.
func policyLine(sub config.CommandPolicy, st syncstate.PolicyState, now time.Time) string {
	e, cached := policyCached(sub, st)
	have := cached && e.RulesTOML != ""
	var parts []string
	switch {
	case !have:
		parts = append(parts, "not yet fetched; no rules from it apply")
		if e.LastError != "" {
			parts = append(parts, "refresh failed: "+e.LastError)
		}
	case now.Sub(e.FetchedAt) > sub.Age():
		parts = append(parts, fmt.Sprintf("unverified (last fetched %s)", policyTime(e.FetchedAt)))
		if e.LastError != "" {
			parts = append(parts, "refresh failed: "+e.LastError+"; keeping last good list")
		}
	case e.LastError != "":
		parts = append(parts, fmt.Sprintf("refresh failed: %s; keeping last good list from %s", e.LastError, policyTime(e.FetchedAt)))
	}
	if !sub.IsCatalog() && have {
		parts = append(parts, policyUnverifiedSource)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("⚠️ satelle: command policy %s %s.", sub.Name, strings.Join(parts, "; "))
}

// policyAdvisoryAt is the session-start decision over explicit inputs: a line
// per subscription that needs saying, and whether one detached refresh should
// start — some list is missing or older than its max_age, and none began within
// the debounce. Several subscriptions share the one refresh.
func policyAdvisoryAt(subs []config.CommandPolicy, st syncstate.PolicyState, now time.Time) (lines []string, refresh bool) {
	for _, sub := range subs {
		if l := policyLine(sub, st, now); l != "" {
			lines = append(lines, l)
		}
		e, cached := policyCached(sub, st)
		if !cached || e.RulesTOML == "" || now.Sub(e.FetchedAt) > sub.Age() {
			refresh = true
		}
	}
	if refresh && !st.CheckedAttempt.IsZero() && now.Sub(st.CheckedAttempt) < substrateRefreshDebounce {
		refresh = false
	}
	return lines, refresh
}

// policyAdvisory is the session-start text, from the local cache alone. With no
// subscription it reads nothing and starts nothing. Without the attempt stamp the
// debounce cannot hold, so no stamp means no spawn.
func policyAdvisory(a *app.App) string {
	subs := a.PlaneConfig().Gate.CommandPolicies
	if len(subs) == 0 {
		return ""
	}
	globalDir, now := config.GlobalDir(), time.Now()
	st, _, _ := syncstate.LoadPolicy(globalDir, a.RepoRoot)
	lines, refresh := policyAdvisoryAt(subs, st, now)
	if refresh && syncstate.MarkPolicyAttempt(globalDir, a.RepoRoot, now) == nil {
		policyRefresh(a.RepoRoot)
	}
	return strings.Join(lines, "\n")
}

// reportPolicyCache prints each subscription's cached state for `satelle
// validate`, from the local cache alone.
func reportPolicyCache(out io.Writer) {
	subs, root, err := policySubscriptions()
	if err != nil || len(subs) == 0 {
		fmt.Fprintln(out, "PASS  command policy (no subscriptions)")
		return
	}
	st, _, _ := syncstate.LoadPolicy(config.GlobalDir(), root)
	now := time.Now()
	for _, sub := range subs {
		if line := policyLine(sub, st, now); line != "" {
			fmt.Fprintf(out, "WARN  %s\n", strings.TrimPrefix(line, "⚠️ satelle: "))
			continue
		}
		fmt.Fprintf(out, "PASS  command policy %s (cached list current)\n", sub.Name)
	}
}

// policyRefreshCmd is the refresh process, run from repoRoot so it loads that
// repo's subscriptions and writes that repo's cache.
func policyRefreshCmd(exe, repoRoot string) *exec.Cmd {
	cmd := exec.Command(exe, "policy", "refresh")
	cmd.Dir = repoRoot
	return cmd
}

// spawnPolicyRefresh starts `satelle policy refresh` detached from the hook: its
// own session, no inherited stdio, and no Wait. Every failure is swallowed.
func spawnPolicyRefresh(repoRoot string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := policyRefreshCmd(exe, repoRoot)
	detachChild(cmd)
	if cmd.Start() != nil {
		return
	}
	_ = cmd.Process.Release()
}
