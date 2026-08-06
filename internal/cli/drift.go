package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// deployedVersionName is the managed stamp of the binary version this repo was
// last init/rebase/restored against. Committed (not gitignored) so clones share
// the heal baseline.
const deployedVersionName = "deployed.version"

// writeDeployedVersion stamps dataDir/deployed.version with the running binary
// version. Returns (true, nil) when the file was created or content changed.
// Called at the end of successful init (and rebase/restore).
func writeDeployedVersion(dataDir string) (bool, error) {
	ver := strings.TrimSpace(buildinfo.Resolve().Version)
	if ver == "" || isDevVersion(ver) {
		return false, nil // never stamp a dev sentinel
	}
	path := filepath.Join(dataDir, deployedVersionName)
	body := fmt.Sprintf("satelle.version: %s\n", ver)
	if prev, err := os.ReadFile(path); err == nil && string(prev) == body {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// readDeployedVersion returns the stamped version, or "" if absent/unreadable.
func readDeployedVersion(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, deployedVersionName))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == "satelle.version:" {
			return strings.TrimSpace(f[1])
		}
	}
	return ""
}

// isDevVersion reports builds that must never self-gate (local make / go run).
// scripts/build-version.sh produces the +<sha>[-dirty] form for unreleased
// make install trees so they hit the '+' rule (sty_022929ef).
func isDevVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return true
	}
	return strings.HasPrefix(v, "0.0.0-dev") || strings.Contains(v, "+")
}

// refuseBreakingDrift fails closed when the installed binary is newer than
// .satelle/deployed.version AND CHANGELOG.md has a ### Breaking entry in the
// open range (deployed, binary]. The REMEDIATION comes from that entry's own
// bullets, not from Go — see breakingDriftError.
// Non-breaking version gaps do not gate. Dev builds never gate.
func refuseBreakingDrift(repoRoot string) error {
	binVer := strings.TrimSpace(buildinfo.Resolve().Version)
	if isDevVersion(binVer) {
		return nil
	}
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	// Uninitialized repo: no .satelle → app.Open may still work zero-config;
	// only gate when the data dir exists (initialized).
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return nil
	}
	deployed := readDeployedVersion(dataDir)
	if deployed == "" {
		// Initialized but never stamped (pre-gate repos): fail closed so the
		// operator runs init once to establish the baseline.
		return fmt.Errorf(
			"satelle: this repo has no .satelle/%s stamp — run `satelle init` to align with binary %s (breaking-surface heal path)",
			deployedVersionName, binVer)
	}
	// Consult changelog for breaking entries in (deployed, binVer].
	entries, err := verb.ChangelogRange(deployed, binVer)
	if err != nil {
		// Missing changelog: do not brick — init analysis still works.
		return nil
	}
	return breakingDriftError(deployed, binVer, entries)
}

// breakingDriftError is the DECISION half of the drift gate: given the repo's
// stamp, the running binary and the changelog entries in (deployed, binVer],
// return the refusal or nil. Split from refuseBreakingDrift so the decision is
// reachable without buildinfo — the dev short-circuit above makes every
// assertion about the whole function vacuous under `go test` (sty_b36c051c).
//
// The remediation is CONFIGURATION: a release that has something specific to
// say says it in its own ### Breaking bullets and those reach the operator
// verbatim. The binary contributes only the preamble and, for an entry that
// declares nothing, the generic `satelle init` fallback. There is deliberately
// no per-release branch here — the next breaking change authors its heal path
// in CHANGELOG.md, with no recompile.
func breakingDriftError(deployed, binVer string, entries []verb.ChangelogEntry) error {
	if verb.CmpSemverExported(deployed, binVer) >= 0 {
		return nil // binary not newer
	}
	if e, ok := firstBreaking(entries); ok {
		head := fmt.Sprintf(
			"satelle: binary %s is ahead of this repo's deployed stamp %s across BREAKING release %s",
			binVer, deployed, e.Version)
		bullets := e.Sections["Breaking"]
		if len(bullets) == 0 {
			return fmt.Errorf("%s — run `satelle init` to heal (see CHANGELOG.md ### Breaking)", head)
		}
		var b strings.Builder
		b.WriteString(head)
		b.WriteString(" — that release says:")
		for _, ln := range bullets {
			b.WriteString("\n  - ")
			b.WriteString(ln)
		}
		return errors.New(b.String())
	}
	return nil
}

// firstBreaking returns the first changelog entry in the range that declares a
// ### Breaking section. Entries arrive newest-first, so that is the NEWEST
// breaking release in the range — the same one the refusal names, which is the
// point. ONE definition of "this gap crosses a breaking release",
// shared by the refusal (breakingDriftError) and the session-start advisory
// (versionDriftLine) so the two can never disagree about the same range.
func firstBreaking(entries []verb.ChangelogEntry) (verb.ChangelogEntry, bool) {
	for _, e := range entries {
		if e.Breaking {
			return e, true
		}
	}
	return verb.ChangelogEntry{}, false
}

// versionDriftAdvisory gathers the inputs versionDriftLine decides on: the
// running binary, this repo's stamp, and the changelog entries between them.
// The gatherer/decision split mirrors refuseBreakingDrift/breakingDriftError —
// the dev short-circuit below makes assertions about the whole function vacuous
// under `go test`, so the decision has to be reachable without buildinfo.
//
// Returns "" for every quiet case. It has NO error channel by construction:
// this feeds `satelle hook context`, and a hook that errors breaks the session
// instead of informing it.
func versionDriftAdvisory(repoRoot string) string {
	binVer := strings.TrimSpace(buildinfo.Resolve().Version)
	if isDevVersion(binVer) {
		return ""
	}
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	// Uninitialised repo: nothing was ever deployed here, so there is no gap to
	// report — same guard refuseBreakingDrift applies.
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return ""
	}
	deployed := readDeployedVersion(dataDir)
	// A missing/unreadable CHANGELOG diverges DELIBERATELY from the refusal path:
	// there it silences the gate (never brick a repo over a missing file), here it
	// only costs the breaking classification. The gap itself is still real and
	// still worth naming, so advise with no entries rather than going quiet.
	entries, err := verb.ChangelogRange(deployed, binVer)
	if err != nil {
		entries = nil
	}
	return versionDriftLine(deployed, binVer, entries)
}

// versionDriftLine is the DECISION half: ONE advisory line when this repo's
// stamp is behind the running binary, "" otherwise. Advisory, never a refusal.
//
// The binary is machine-wide, so `satelle update` in one repo moves every repo
// onto the new binary while each repo's stamp keeps naming the binary that last
// deployed its scaffolding. That gap is harmless until a release in the open
// range (deployed, binVer] declares ### Breaking — at which point the first
// store-backed verb fails closed, and at session start that verb is the
// SessionStart hook's own reindex. This line is the warning ahead of that
// refusal.
//
// Deliberately ONE line and no bullet list: the refusal path already prints the
// release's own ### Breaking bullets verbatim, and duplicating them here would
// be a per-session token toll in every repo. Silent when the stamp matches, for
// the same reason.
func versionDriftLine(deployed, binVer string, entries []verb.ChangelogEntry) string {
	if strings.TrimSpace(binVer) == "" {
		return ""
	}
	if deployed == "" {
		// Initialised but never stamped: the first store-backed verb REFUSES this
		// repo outright, so the unstamped case is exactly the one worth warning
		// about ahead of time.
		return fmt.Sprintf(
			"\u26a0\ufe0f satelle: binary %s — this repo has no .satelle/%s stamp, so the next store-backed verb will refuse it; run `satelle init` to establish the baseline.",
			binVer, deployedVersionName)
	}
	if verb.CmpSemverExported(deployed, binVer) >= 0 {
		return "" // stamp current (or ahead) — the common path stays silent
	}
	gap := "no breaking release in that range"
	if e, ok := firstBreaking(entries); ok {
		gap = "BREAKING release " + e.Version + " in that range"
	}
	return fmt.Sprintf(
		"\u26a0\ufe0f satelle: binary %s is ahead of this repo's .satelle/%s stamp %s (%s) — run `satelle init` to heal.",
		binVer, deployedVersionName, deployed, gap)
}
