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
	for _, e := range entries {
		if !e.Breaking {
			continue
		}
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
