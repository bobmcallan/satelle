// `satelle update` is an inline self-updater (like `claude update`): it resolves
// the latest GitHub release, and if newer than the installed binary, downloads
// the platform asset, sha256-verifies it, and atomically replaces the installed
// binary — the SAME asset/checksum/install-dir scheme as scripts/install.sh, so
// the two never drift. If the background service is running it is restarted onto
// the new binary.
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
)

const updateRepo = "bobmcallan/satelle"

func init() {
	var check, noRestart, local, force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update satelle to the latest release (--local pins it under this repo's .satelle/)",
		Long: `update resolves the latest GitHub release and, if it differs from the
installed binary, downloads the platform asset, sha256-verifies it, and replaces
the installed binary in place — the same asset/checksum/location scheme as the
curl installer. If the background service is running it is restarted onto the new
binary. --check reports availability without installing.

A matching version string is not enough proof the installed bytes match the
published release. When the versions already match, update compares the local
file's sha256 against the published <asset>.sha256 and reinstalls on a mismatch
(for example a retagged version or a machine-wide make install of unreleased
code). --force reinstalls the published asset even when both the version and the
checksum already match — the recovery path when a global binary carries an
unreleased build and the version strings look equal.

--local installs the release into THIS repo's .satelle/satelle instead of the
global install dir; a present .satelle/satelle then takes precedence (satelle
re-execs it) so the repo runs its own pinned binary. --local never restarts the
global service.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force && check {
				return fmt.Errorf("--force and --check conflict: --check changes nothing")
			}
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			target := installTarget()
			if local {
				target = repoLocalTarget()
			}
			latest, err := latestReleaseTag(ctx, updateRepo)
			if err != nil {
				return fmt.Errorf("resolve latest release: %w", err)
			}
			current, currentCommit := installedBanner(target)
			cliUpdated := false
			// cliReplaced tracks the CLI BINARY specifically: deployed scaffolding is
			// keyed to the CLI version, so a serve-only refresh does not stale the
			// estate and must not print the estate guidance (sty_0f471251).
			cliReplaced := false

			needCLI := force || updateAvailable(current, latest)
			if !needCLI {
				// Versions match — prove the installed bytes are the published asset
				// (sty_1cd2ff01). A string match alone is not enough.
				localSum, pubSum, art, artErr := resolveArtifactIdentity(ctx, updateRepo, latest, "satelle", target)
				switch art {
				case artifactMatch:
					fmt.Fprintf(out, "CLI already up to date (%s, sha256 %s matches published asset)\n",
						formatBanner(current, currentCommit), shortSum(localSum))
				case artifactDiffer:
					// Action word only when we will actually reinstall; --check is read-only.
					action := " — reinstalling"
					if check {
						action = " — run `satelle update`"
					}
					fmt.Fprintf(out, "CLI %s installed build differs from published %s (installed sha256 %s, published %s)%s\n",
						formatBanner(current, currentCommit), latest, shortSum(localSum), shortSum(pubSum), action)
					needCLI = true
				default: // artifactUnknown
					fmt.Fprintf(out, "CLI version matches (%s) but identity NOT verified: %v — run `satelle update --force` to reinstall the published asset\n",
						formatBanner(current, currentCommit), artErr)
				}
			}
			if needCLI {
				if check {
					// Differ already printed the remediation; version-diff still needs the classic line.
					if updateAvailable(current, latest) || force {
						fmt.Fprintf(out, "update available: %s → %s  (run `satelle update`)\n", current, latest)
					}
				} else {
					if force && !updateAvailable(current, latest) {
						fmt.Fprintf(out, "forcing reinstall of %s (%s)\n", target, latest)
					} else if updateAvailable(current, latest) {
						fmt.Fprintf(out, "updating %s: %s → %s\n", target, current, latest)
					}
					if err := downloadAndReplace(ctx, updateRepo, latest, target); err != nil {
						return err
					}
					fmt.Fprintf(out, "installed %s (%s)\n", target, latest)
					cliUpdated = true
					cliReplaced = true
				}
			}
			if check {
				// Surface serve channel too when independent (sty_19ff03f4), including
				// the artifact-identity verdict so dogfood can read it (sty_1cd2ff01).
				if !local {
					if st, serr := latestServeReleaseTag(ctx, updateRepo); serr == nil {
						serveTarget := installedDaemonPath(target)
						installedServe, servePresent := serveInstalledVersion(serveTarget)
						if !servePresent {
							fmt.Fprintf(out, "latest serve release: %s (not installed)\n", st)
						} else {
							localSum, pubSum, art, artErr := resolveDaemonIdentity(ctx, updateRepo, st, serveTarget)
							switch art {
							case artifactMatch:
								fmt.Fprintf(out, "latest serve release: %s (installed %s, sha256 %s matches published asset)\n",
									st, installedServe, shortSum(localSum))
							case artifactDiffer:
								fmt.Fprintf(out, "latest serve release: %s (installed build differs: installed sha256 %s, published %s — run `satelle update`)\n",
									st, shortSum(localSum), shortSum(pubSum))
							default:
								fmt.Fprintf(out, "latest serve release: %s (installed %s; identity NOT verified: %v)\n",
									st, installedServe, artErr)
							}
						}
					}
				}
				return nil
			}
			// Always refresh sibling satelled from serve-v* even when CLI was current.
			if !local {
				serveTarget := daemonInstallPath(target)
				installedAt := installedDaemonPath(target)
				serveTag, serr := latestServeReleaseTag(ctx, updateRepo)
				installedServe, servePresent := serveInstalledVersion(installedAt)
				_, serveCommit := installedServeBanner(installedAt)
				var art artifactIdentity
				var localSum, pubSum string
				var artErr error
				if force {
					art = artifactDiffer // force reinstall when a release resolves
				} else if serr == nil && servePresent {
					localSum, pubSum, art, artErr = resolveDaemonIdentity(ctx, updateRepo, serveTag, installedAt)
				}
				switch outcome := classifyServeOutcome(installedServe, serveTag, serr, servePresent, art); outcome {
				case serveCurrent:
					fmt.Fprintf(out, "satelled already up to date (%s, sha256 %s matches published asset)\n",
						serveTag, shortSum(localSum))
				case serveUnverified:
					fmt.Fprintf(out, "satelled version matches (%s) but identity NOT verified: %v — run `satelle update --force` to reinstall the published asset\n",
						formatBanner(installedServe, serveCommit), artErr)
				case serveAbsentNoRelease:
					// A fork that has never published a serve release, on a machine
					// with no daemon binary: nothing to install and nothing wrong.
					fmt.Fprintf(out, "satelled not installed and no serve release published — nothing to update\n")
				case serveFail:
					// A serve release that cannot be RESOLVED is a failure, not a
					// skip: reporting exit 0 here is what let a release read green
					// while the live service stayed on an older daemon binary
					// (sty_0dcedb0d). Same rule the CLI half already follows.
					return fmt.Errorf("satelled update failed: %w", serr)
				default:
					if art == artifactDiffer && !force && !updateAvailable(installedServe, tagVersion(serveTag)) {
						fmt.Fprintf(out, "satelled %s installed build differs from published %s (installed sha256 %s, published %s) — reinstalling\n",
							formatBanner(installedServe, serveCommit), serveTag, shortSum(localSum), shortSum(pubSum))
					} else if force {
						fmt.Fprintf(out, "forcing reinstall of %s (%s)\n", serveTarget, serveTag)
					}
					if err := downloadDaemon(ctx, updateRepo, serveTag, serveTarget); err != nil {
						return fmt.Errorf("satelled update failed (%s): %w", serveTag, err)
					}
					fmt.Fprintf(out, "installed %s (%s)\n", serveTarget, serveTag)
					cliUpdated = true // restart so serve process can pick up sibling if re-exec path
				}
			}
			// The global service runs the global binary; a repo-local pin does not
			// drive it, so only restart for a global update.
			if cliUpdated && !noRestart && !local {
				// A cycle that was ATTEMPTED and could not be confirmed fails the
				// verb: update must never report an installed-and-live release it
				// did not verify (sty_f20f3f3b).
				if err := restartServiceIfRunning(out); err != nil {
					return err
				}
			}
			printEstateGuidance(out, cliReplaced, local)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report whether an update is available without installing")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart the background service after updating")
	cmd.Flags().BoolVar(&local, "local", false, "install into this repo's .satelle/satelle (a repo-local pin) instead of the global binary")
	cmd.Flags().BoolVar(&force, "force", false, "reinstall the published asset even when the reported versions already match")
	register(cmd)
}

// printEstateGuidance tells the operator that upgrading the binary just
// invalidated the deployed scaffolding of every OTHER registered repo
// (sty_0f471251). Nothing used to say so, leaving a working binary, a
// majority-stale estate, and no prompt.
//
// The staleness is not cosmetic: `satelle workspace add` refuses in a stale repo,
// which wedges the serve mirror against those partitions — a failure the operator
// will not connect back to the upgrade.
//
// Gated on cliReplaced, NOT cliUpdated: deployed scaffolding is keyed to the CLI
// version, so a serve-only refresh does not stale the estate. A no-op update, a
// --check, and a repo-local pin (which governs one repo, not the estate) all stay
// quiet too.
func printEstateGuidance(out io.Writer, cliReplaced, local bool) {
	if !cliReplaced || local {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Repos you already have are not migrated automatically —")
	fmt.Fprintln(out, "scaffolding deployed by the previous binary is now stale:")
	fmt.Fprintln(out, "  satelle doctor --all      # read-only: which registered repos are stale")
	fmt.Fprintln(out, "  satelle init --all        # dry-run: what healing them would change")
}

// installTarget is the binary update replaces: SATELLE_INSTALL_DIR (else
// ~/.local/bin)/satelle — the same location scripts/install.sh writes.
func installTarget() string {
	dir := os.Getenv("SATELLE_INSTALL_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "bin")
	}
	return filepath.Join(dir, "satelle")
}

// installedVersion returns the version the target binary reports, or the running
// build's version if the target can't be run (e.g. not installed yet).
func installedVersion(target string) string {
	ver, _ := installedBanner(target)
	if ver != "" {
		return ver
	}
	return buildinfo.Resolve().Version
}

// installedBanner returns the version and commit the target CLI binary reports
// (`satelle version`). Missing fields are empty. One parser for both fields so
// installedVersion cannot drift from the commit half (sty_1cd2ff01 review).
func installedBanner(target string) (version, commit string) {
	out, err := exec.Command(target, "version").Output()
	if err != nil {
		return "", ""
	}
	return parseVersionBanner(string(out))
}

// installedServeBanner is the serve sibling of installedBanner
// (`satelled --version`).
func installedServeBanner(target string) (version, commit string) {
	out, err := exec.Command(target, "--version").Output()
	if err != nil {
		return "", ""
	}
	return parseVersionBanner(string(out))
}

// parseVersionBanner extracts version (field 2) and the token after "commit "
// from a banner like `satelle 0.0.6 (commit abc123, built …)` or
// `satelled 0.0.12 (commit abc, built now)`.
func parseVersionBanner(out string) (version, commit string) {
	if fields := strings.Fields(out); len(fields) >= 2 {
		version = fields[1]
	}
	const marker = "commit "
	if i := strings.Index(out, marker); i >= 0 {
		rest := out[i+len(marker):]
		if end := strings.IndexAny(rest, ",)\n \t"); end >= 0 {
			commit = rest[:end]
		} else {
			commit = strings.TrimSpace(rest)
		}
	}
	return version, commit
}

// formatBanner joins version and optional commit for operator messages.
func formatBanner(version, commit string) string {
	if commit == "" {
		return version
	}
	return version + ", commit " + commit
}

// serveOutcome is what `satelle update` should do about the sibling
// satelled binary. The three cases used to collapse into one printed
// "skipped" line at exit 0, so an unresolvable release looked exactly like a
// no-op and a release could report success while the live service ran an older
// serve binary (sty_0dcedb0d).
type serveOutcome int

const (
	// serveInstall — a serve release resolved and differs from what is installed
	// (by version string OR by artifact checksum — sty_1cd2ff01).
	serveInstall serveOutcome = iota
	// serveCurrent — the installed serve binary already IS the resolved release
	// (version match AND artifact checksum match).
	serveCurrent
	// serveFail — the release could not be resolved; the verb must fail.
	serveFail
	// serveAbsentNoRelease — no serve release exists AND no serve binary is
	// installed: nothing to do, and nothing wrong.
	serveAbsentNoRelease
	// serveUnverified — versions match but the published artifact checksum
	// could not be compared (offline, 404, unreadable local file). Skip
	// reinstall; never claim "up to date" (sty_1cd2ff01).
	serveUnverified
)

// classifyServeOutcome decides which outcome applies. installedVer is what the
// installed serve binary reports ("" when it could not be read — treated as
// unknown, never as current). art is the artifact-checksum verdict when the
// version strings already match; callers pass artifactMatch when force is set
// is not in play and identity was not computed (absent binary / discovery fail
// paths ignore art). Pure for unit tests.
func classifyServeOutcome(installedVer, serveTag string, discoveryErr error, servePresent bool, art artifactIdentity) serveOutcome {
	if discoveryErr != nil {
		if !servePresent && isNoServeReleaseErr(discoveryErr) {
			return serveAbsentNoRelease
		}
		return serveFail
	}
	if installedVer != "" && !updateAvailable(installedVer, tagVersion(serveTag)) {
		switch art {
		case artifactMatch:
			return serveCurrent
		case artifactDiffer:
			return serveInstall
		default:
			return serveUnverified
		}
	}
	return serveInstall
}

// artifactIdentity is the result of comparing an installed binary's bytes to
// the published release asset checksum. Named "artifact" deliberately — the
// package already has identityVerdict/statIdentity for process-exe (dev+inode)
// comparisons; those are a different concept (sty_1cd2ff01 architecture review).
type artifactIdentity int

const (
	artifactMatch artifactIdentity = iota
	artifactDiffer
	artifactUnknown
)

// compareArtifactIdentity is pure: match when both sums present and equal,
// differ when both present and unequal, unknown on any error or empty sum.
func compareArtifactIdentity(localSum, publishedSum string, err error) artifactIdentity {
	if err != nil || localSum == "" || publishedSum == "" {
		return artifactUnknown
	}
	if strings.EqualFold(localSum, publishedSum) {
		return artifactMatch
	}
	return artifactDiffer
}

// parseChecksumLine extracts the leading hex field from a sha256 line
// ("<hex>  <name>"). Shared by verifyChecksum and publishedChecksum.
func parseChecksumLine(shaLine string) string {
	want := strings.TrimSpace(shaLine)
	if i := strings.IndexAny(want, " \t"); i > 0 {
		want = want[:i]
	}
	return want
}

// fileChecksum returns the sha256 hex of the file at path.
func fileChecksum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// publishedChecksum GETs the published <asset>.sha256 for binary@tag and
// returns the hex sum. Uses SATELLE_RELEASE_BASE when set (mirrors, tests).
func publishedChecksum(ctx context.Context, repo, tag, binary string) (string, error) {
	base := os.Getenv("SATELLE_RELEASE_BASE")
	if base == "" {
		base = fmt.Sprintf("https://github.com/%s/releases/download", repo)
	}
	name := assetNameFor(binary, tag)
	line, err := httpGetBytes(ctx, base+"/"+tag+"/"+name+".sha256")
	if err != nil {
		return "", err
	}
	sum := parseChecksumLine(string(line))
	if sum == "" {
		return "", fmt.Errorf("empty checksum for %s", name)
	}
	return sum, nil
}

// resolveArtifactIdentity compares local file bytes to the published asset
// checksum. reason is set when the verdict is artifactUnknown.
func resolveArtifactIdentity(ctx context.Context, repo, tag, binary, localPath string) (local, published string, id artifactIdentity, reason error) {
	local, lerr := fileChecksum(localPath)
	if lerr != nil {
		return "", "", artifactUnknown, lerr
	}
	published, perr := publishedChecksum(ctx, repo, tag, binary)
	if perr != nil {
		return local, "", artifactUnknown, perr
	}
	return local, published, compareArtifactIdentity(local, published, nil), nil
}

// shortSum returns the first 8 hex chars of a checksum for operator messages.
func shortSum(sum string) string {
	if len(sum) <= 8 {
		return sum
	}
	return sum[:8]
}

// tagVersion reduces a release tag to the bare version the binary reports:
// serve-v0.0.12 → 0.0.12, v0.0.368 → 0.0.368.
func tagVersion(tag string) string {
	return normVer(strings.TrimPrefix(strings.TrimSpace(tag), "serve-"))
}

// isNoServeReleaseErr reports whether err means "this repo has no serve release
// at all" rather than "the lookup failed". Only the former is a legitimate no-op.
func isNoServeReleaseErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no published release with prefix")
}

// serveInstalledVersion reports the version the installed satelled binary
// prints (`--version`, the same flag the release workflow validates with) and
// whether that binary exists at all. An unparseable answer yields ("", true):
// present but unknown, which classifies as install rather than current.
func serveInstalledVersion(target string) (version string, present bool) {
	if _, err := os.Stat(target); err != nil {
		return "", false
	}
	ver, _ := installedServeBanner(target)
	if ver == "" {
		// Present but unreadable / unparseable — never treat as current.
		return "", true
	}
	return ver, true
}

// assetName is the release asset filename for this platform — identical to the
// name install.sh derives (Go's GOOS/GOARCH already match the published amd64/
// arm64 + linux/darwin asset suffixes).
func assetName(tag string) string {
	return assetNameFor("satelle", tag)
}

// assetNameFor builds a release asset name for binary (satelle | satelled).
// Tags may be vX (CLI) or serve-vY (serve); assets always use the v-prefixed version.
func assetNameFor(binary, tag string) string {
	ver := tag
	if strings.HasPrefix(tag, "serve-") {
		ver = strings.TrimPrefix(tag, "serve-") // serve-v0.0.1 → v0.0.1
	}
	name := fmt.Sprintf("%s-%s-%s-%s", binary, ver, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// updateAvailable reports whether latest differs from the installed version
// (normalising a leading v). GitHub's "latest" release is newest by definition,
// so any difference means an update is available.
func updateAvailable(current, latest string) bool {
	return normVer(current) != normVer(latest)
}

func normVer(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// latestReleaseTag queries the release API for the latest tag. The API URL is
// the GitHub default, overridable via SATELLE_RELEASE_API (mirrors, tests).
func latestReleaseTag(ctx context.Context, repo string) (string, error) {
	url := os.Getenv("SATELLE_RELEASE_API")
	if url == "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	}
	body, err := httpGetBytes(ctx, url)
	if err != nil {
		return "", err
	}
	return parseLatestTag(body)
}

func parseLatestTag(body []byte) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("no tag_name in release response")
	}
	return r.TagName, nil
}

// releasePageSize / maxReleasePages bound the serve-release walk. Serve releases
// are rare and CLI releases frequent, so the newest serve release routinely sits
// far down the newest-first list: reading one page of 30 made discovery MISS a
// published release entirely once ~30 CLI releases followed it, and report that
// as "no release with prefix serve-v" (sty_0dcedb0d). One page of 100 answers
// almost always; the cap is the backstop, and exhausting it is reported as such
// rather than as an absence.
const (
	releasePageSize = 100
	maxReleasePages = 10
)

// latestServeReleaseTag finds the newest published serve-v* release
// (sty_19ff03f4). Serve releases are published with --latest=false so
// /releases/latest stays CLI, which is why they must be found by listing.
func latestServeReleaseTag(ctx context.Context, repo string) (string, error) {
	base := os.Getenv("SATELLE_RELEASE_LIST_API")
	if base == "" {
		base = fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)
	}
	return firstPrefixedTagInPages(func(page int) ([]byte, error) {
		return httpGetBytes(ctx, releaseListPageURL(base, page))
	}, "serve-v", maxReleasePages)
}

// releaseListPageURL adds pagination to the release-list base URL, merging with
// any query the base already carries (SATELLE_RELEASE_LIST_API overrides may).
func releaseListPageURL(base string, page int) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("per_page", strconv.Itoa(releasePageSize))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

// firstPrefixedTagInPages walks pages of the newest-first release list until it
// finds a prefix match, a page comes back empty (the list is exhausted), or
// maxPages is reached. An exhausted cap is a DISTINCT error from an exhausted
// list: "searched N pages" says the answer may exist further back, which a bare
// "not found" would hide. Pure over fetch for unit tests.
func firstPrefixedTagInPages(fetch func(page int) ([]byte, error), prefix string, maxPages int) (string, error) {
	for page := 1; page <= maxPages; page++ {
		body, err := fetch(page)
		if err != nil {
			return "", err
		}
		tag, n, err := firstPrefixedTagOnPage(body, prefix)
		if err != nil {
			return "", err
		}
		if tag != "" {
			return tag, nil
		}
		if n < releasePageSize {
			// Short (or empty) page — that was the end of the list.
			return "", fmt.Errorf("no published release with prefix %q", prefix)
		}
	}
	return "", fmt.Errorf("no published release with prefix %q in the newest %d releases", prefix, maxPages*releasePageSize)
}

// firstPrefixedTag returns the first tag_name in a GitHub releases JSON array
// that has the given prefix (newest-first list). Pure for unit tests.
func firstPrefixedTag(body []byte, prefix string) (string, error) {
	tag, _, err := firstPrefixedTagOnPage(body, prefix)
	if err != nil {
		return "", err
	}
	if tag == "" {
		return "", fmt.Errorf("no release tag with prefix %q", prefix)
	}
	return tag, nil
}

// firstPrefixedTagOnPage returns the first matching tag on one page plus how
// many entries that page held (so the caller knows whether to keep walking).
// tag is "" when the page holds no match. DRAFTS ARE SKIPPED: a draft carries no
// downloadable asset, so selecting one would trade a missed release for a 404.
func firstPrefixedTagOnPage(body []byte, prefix string) (tag string, count int, err error) {
	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", 0, err
	}
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if strings.HasPrefix(r.TagName, prefix) {
			return r.TagName, len(releases), nil
		}
	}
	return "", len(releases), nil
}

// downloadAndReplace downloads the platform asset for tag from repo's releases,
// verifies its sha256, and atomically replaces target. The download base is the
// GitHub default, overridable via SATELLE_RELEASE_BASE (mirrors, tests).
func downloadAndReplace(ctx context.Context, repo, tag, target string) error {
	return downloadAndReplaceNamed(ctx, repo, tag, "satelle", target)
}

// downloadAndReplaceNamed downloads a named binary asset (satelle | satelled).
func downloadAndReplaceNamed(ctx context.Context, repo, tag, binary, target string) error {
	base := os.Getenv("SATELLE_RELEASE_BASE")
	if base == "" {
		base = fmt.Sprintf("https://github.com/%s/releases/download", repo)
	}
	return downloadAndReplaceFrom(ctx, base+"/"+tag, assetNameFor(binary, tag), target)
}

// daemonInstallPath is where a newly installed daemon binary lands (always satelled).
func daemonInstallPath(cliTarget string) string {
	p := filepath.Join(filepath.Dir(cliTarget), buildinfo.DaemonName)
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	return p
}

// installedDaemonPath is the existing daemon binary if any: satelled first,
// then the legacy satelle-serve sibling (compatibility fallback, not a name).
func installedDaemonPath(cliTarget string) string {
	primary := daemonInstallPath(cliTarget)
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	legacy := filepath.Join(filepath.Dir(cliTarget), buildinfo.LegacyDaemonName)
	if runtime.GOOS == "windows" {
		legacy += ".exe"
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

// downloadDaemon fetches the satelled asset, then the legacy satelle-serve
// asset name on older serve-v* tags (compatibility fallback, not a name).
func downloadDaemon(ctx context.Context, repo, tag, target string) error {
	if err := downloadAndReplaceNamed(ctx, repo, tag, buildinfo.DaemonName, target); err == nil {
		return nil
	}
	return downloadAndReplaceNamed(ctx, repo, tag, buildinfo.LegacyDaemonName, target)
}

// resolveDaemonIdentity compares against the satelled asset, then the legacy
// satelle-serve asset name when the new name is unpublished on that tag.
func resolveDaemonIdentity(ctx context.Context, repo, tag, localPath string) (local, published string, id artifactIdentity, reason error) {
	local, published, id, reason = resolveArtifactIdentity(ctx, repo, tag, buildinfo.DaemonName, localPath)
	if id != artifactUnknown {
		return local, published, id, reason
	}
	return resolveArtifactIdentity(ctx, repo, tag, buildinfo.LegacyDaemonName, localPath)
}

// downloadAndReplaceFrom is the injectable core: baseURL/<name> is the binary,
// baseURL/<name>.sha256 the checksum. Split out so tests serve local fixtures.
func downloadAndReplaceFrom(ctx context.Context, baseURL, name, target string) error {
	bin, err := httpGetBytes(ctx, baseURL+"/"+name)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	shaLine, err := httpGetBytes(ctx, baseURL+"/"+name+".sha256")
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if err := verifyChecksum(bin, string(shaLine)); err != nil {
		return err
	}
	return replaceExecutable(target, bin)
}

func httpGetBytes(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum checks data against the sha256 in shaLine ("<hex>  <name>").
func verifyChecksum(data []byte, shaLine string) error {
	want := parseChecksumLine(shaLine)
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("sha256 mismatch (want %s, got %s) — aborting, binary unchanged", want, got)
	}
	return nil
}

// replaceExecutable atomically replaces target with data: write a temp file in
// the same directory, chmod 0755, then rename over target. A running copy keeps
// its open inode, so a live process is unaffected until it restarts.
func replaceExecutable(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".satelle-update-*")
	if err != nil {
		return fmt.Errorf("write %s: %w (is the install dir writable? set SATELLE_INSTALL_DIR)", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// restartServiceIfRunning restarts the background service onto the new binary,
// SUDO-FREE, whatever supervises it, and VERIFIES the restart by exe identity
// (dev+inode of the running process's binary) rather than trusting a restart
// command's exit code or a version string that may not have changed (sty_c344d080
// AC1). It tries the systemd USER unit first (with the user-session env defaulted,
// so a headless/non-login shell can reach the bus); then, if the user unit is not
// usable but the SYSTEM unit is active, it restarts without sudo by SIGTERM-ing the
// unit's MainPID — the process runs as the operator (User=), and the system unit's
// Restart=always makes systemd respawn it onto the new binary (sty_1ac9f095). When
// NEITHER systemctl path is reachable (a broken user D-Bus is a known WSL defect),
// the running process is still located by cgroup or listening-port inspection
// (AC2) so the operator gets a precise diagnosis — service absent, service stale
// with an unreachable supervisor, or a start-limited unit — instead of one generic
// line covering three different problems (AC3, AC4). A genuine sudo restart stays
// the operator's fallback when nothing here can act.
func restartServiceIfRunning(out io.Writer) error {
	return restartServiceIfRunningRoot(out, "/proc")
}

// restartHooks are the seams restartServiceIfRunningRoot calls through instead of
// the real systemctl-touching functions directly. Hermetic tests override these
// (restoring via t.Cleanup) so no test can ever shell out to the operator's actual
// systemctl / actual satelle.service unit (AC6, the plan's "avoid real systemd in
// unit tests") — the fake /proc tree alone is not enough, since the real functions
// name the real serviceUnitName constant. Defaults are the real implementations.
var restartHooks = struct {
	lookSystemctl        func() error
	userUnitActive       func() bool
	userUnitMainPID      func() int
	restartUserUnit      func() error
	systemUnitMainPID    func() int
	systemUnitRestartAlw func() bool
	signalPID            func(int, syscall.Signal) error
	systemUnitRespawned  func(int) (int, bool)
	systemUnitStartLtd   func() bool
}{
	lookSystemctl:        func() error { _, err := exec.LookPath("systemctl"); return err },
	userUnitActive:       userUnitActive,
	userUnitMainPID:      userUnitMainPID,
	restartUserUnit:      func() error { return runCaptureEnv(userEnv(), "systemctl", "--user", "restart", serviceUnitName) },
	systemUnitMainPID:    systemUnitMainPID,
	systemUnitRestartAlw: systemUnitRestartAlways,
	signalPID:            signalPID,
	systemUnitRespawned:  systemUnitRespawned,
	systemUnitStartLtd:   systemUnitStartLimited,
}

// restartServiceIfRunningRoot is the injectable core: procRoot lets hermetic tests
// substitute a fake /proc tree (AC6) without touching the real system, and every
// systemctl-touching step routes through restartHooks for the same reason.
func restartServiceIfRunningRoot(out io.Writer, procRoot string) error {
	if runtime.GOOS != "linux" {
		return nil // cgroup/port discovery and /proc identity are Linux-only (WSL is the story context)
	}
	if restartHooks.lookSystemctl() != nil {
		return nil
	}
	wanted, wantedOK := wantedExeIdentity(procRoot)

	// 1) systemd USER unit — sudo-free when the user manager is alive. Verify by
	// identity, not by the restart command's exit code alone.
	if restartHooks.userUnitActive() {
		if err := restartHooks.restartUserUnit(); err == nil {
			pid := restartHooks.userUnitMainPID()
			reportRestartOutcome(out, "user unit", pid, waitForIdentity(procRoot, pid, wanted, wantedOK))
			return nil
		}
	}
	// 2) SYSTEM unit — sudo-free restart by signalling MainPID; Restart=always respawns.
	if pid := restartHooks.systemUnitMainPID(); pid > 0 {
		// Only signal a unit that will RESPAWN — a clean SIGTERM to a Restart=on-failure
		// unit would STOP the service, not reload it. Never leave a good release with a
		// dead service; guide the operator to a persistent supervisor instead.
		if !restartHooks.systemUnitRestartAlw() {
			fmt.Fprintf(out, "system unit %s is not Restart=always — a signal would stop it, not respawn. Upgrade it with `satelle service install --system`, or restart manually: sudo systemctl restart %s\n", serviceUnitName, serviceUnitName)
			return nil
		}
		if err := restartHooks.signalPID(pid, syscall.SIGTERM); err == nil {
			if newPID, respawned := restartHooks.systemUnitRespawned(pid); respawned {
				reportRestartOutcome(out, "system unit", newPID, waitForIdentity(procRoot, newPID, wanted, wantedOK))
				return nil
			}
			if restartHooks.systemUnitStartLtd() {
				fmt.Fprintf(out, "%s is start-limited (systemd exhausted its restart attempts and gave up) — run: systemctl reset-failed %s && sudo systemctl restart %s\n", serviceUnitName, serviceUnitName, serviceUnitName)
				return nil
			}
			fmt.Fprintf(out, "signalled %s (pid %d) but it did not respawn — restart manually: sudo systemctl restart %s\n", serviceUnitName, pid, serviceUnitName)
			return nil
		}
	}
	// 3) Neither systemctl path is reachable. Locate the live process WITHOUT the
	// bus (cgroup, then listening-port) so "no service configured" and "a service
	// IS running but its supervisor is unreachable" are never the same message.
	livePID := findPIDByCgroup(procRoot, serviceUnitName)
	if livePID == 0 {
		livePID = findPIDByListenPort(procRoot, servicePort())
	}
	if livePID == 0 {
		fmt.Fprintf(out, "binary updated, but no running %s process was found — start one with `satelle service install` (user) or `satelle service install --system` (persistent)\n", serviceUnitName)
		return nil
	}
	live, liveOK := identityFromPID(procRoot, livePID)
	if wantedOK && liveOK && identitiesMatch(live, wanted) {
		fmt.Fprintf(out, "%s (pid %d) is already running the new binary — no restart needed\n", serviceUnitName, livePID)
		return nil
	}
	// A stale process is running and its supervisor cannot be COMMANDED from here —
	// but it does not need to be. A supervisor respawns its own child from the unit
	// file autonomously when that child exits; D-Bus is only the channel an EXTERNAL
	// actor uses to REQUEST a restart. So terminate the stale process and let the
	// supervisor do the rest, on the newly installed binary (sty_f20f3f3b).
	return busFreeRestart(out, procRoot, livePID, wanted, wantedOK)
}

// restartSignalForPolicy maps a unit's Restart= policy to the signal that makes
// systemd RESPAWN that unit — or reports that no signal does (sty_d45618d5).
//
// This is the correction to v0.0.361, which sent SIGTERM first and escalated to
// SIGKILL. systemd classifies SIGTERM (with SIGHUP/SIGINT/SIGPIPE) as a CLEAN
// exit, so an on-failure unit does not respawn from it — it STOPS, permanently,
// and the escalation cannot recover it because the process is already gone. A
// signal is therefore only ever sent when its effect on THIS policy is known:
//
//   - always     → SIGTERM. Graceful, and it respawns.
//   - on-failure → SIGKILL. The only signal systemd counts as a failure, hence
//     the only one this unit respawns from. Not an escalation — the correct
//     first and only signal for this policy.
//   - anything else (no, absent, unreadable, or a conditional policy whose
//     respawn semantics are not established: on-abnormal, on-abort, on-success,
//     on-watchdog) → NO signal. Stopping a service nothing will restart is
//     strictly worse than leaving it stale.
//
// known distinguishes "the unit declares Restart=no" from "we could not read the
// unit at all". Both forbid signalling, so the signal decision is the same; the
// caller keeps the distinction for its message.
func restartSignalForPolicy(policy string, known bool) (syscall.Signal, bool) {
	if !known {
		return 0, false
	}
	switch policy {
	case "always":
		return syscall.SIGTERM, true
	case "on-failure":
		return syscall.SIGKILL, true
	}
	return 0, false
}

// printInstallRemedy names the one-time fix for a service nothing would respawn.
// Shared by both refusal branches so "the same remedy" is structural rather than
// two strings that can drift apart.
func printInstallRemedy(out io.Writer) {
	fmt.Fprintf(out, "  satelle service install            (user unit; needs lingering)\n")
	fmt.Fprintf(out, "  satelle service install --system   (persistent system unit)\n")
}

// busFreeRestart cycles a supervised process without any systemctl call: signal
// it with the ONE signal its unit's Restart policy respawns from, let the
// supervisor restart it from the unit file, then CONFIRM from kernel facts. It
// never starts a process itself — an ephemeral relaunch would satisfy a version
// check while breaking the durability the release contract requires
// (sty_f20f3f3b), and it never sends a signal whose effect is unknown
// (sty_d45618d5).
func busFreeRestart(out io.Writer, procRoot string, livePID int, wanted exeIdentity, wantedOK bool) error {
	sup, ok := persistentSupervisor(procRoot, livePID)
	if !ok || !sup.Persistent {
		// Nothing would restart it. Signalling here would take the service DOWN and
		// leave it down — strictly worse than stale. Change nothing, say so.
		owner := "its parent is not a systemd manager"
		if ok && sup.Name == "systemd" {
			owner = "its systemd user manager is not lingering, so it dies with the login session"
		}
		fmt.Fprintf(out, "%s (pid %d) is running on the OLD binary, but no persistent supervisor owns it (%s) — a restart would not be respawned, so it was left running. Install a persistent unit, then re-run `satelle update`:\n", serviceUnitName, livePID, owner)
		printInstallRemedy(out)
		return nil
	}

	policy, known := unitRestartPolicy()
	sig, willRespawn := restartSignalForPolicy(policy, known)
	if !willRespawn {
		why := fmt.Sprintf("its unit declares Restart=%s", policy)
		if !known {
			why = "its unit file could not be read, so its Restart policy is unknown"
		} else if policy == "" {
			why = "its unit declares no Restart policy"
		}
		fmt.Fprintf(out, "%s (pid %d) is running on the OLD binary, but %s — no signal would make systemd respawn it, so it was left running rather than stopped. Install a unit that restarts, then re-run `satelle update`:\n", serviceUnitName, livePID, why)
		printInstallRemedy(out)
		return nil
	}

	start := time.Now()
	fmt.Fprintf(out, "%s (pid %d) is on the old binary; its supervisor (%s, pid %d) is unreachable by bus but Restart=%s respawns on %s — cycling\n",
		serviceUnitName, livePID, sup.Name, sup.PID, policy, sig)

	newPID, ok := signalAndAwaitRespawn(procRoot, livePID, sig)
	if !ok {
		// No second, different signal: the policy already told us which one
		// respawns, so a failure here is a failure to converge, not a reason to
		// try a more destructive one.
		return fmt.Errorf("%s (pid %d) did not respawn after %s — no replacement process is holding the service; start it with `satelle service install --system`",
			serviceUnitName, livePID, time.Since(start).Round(time.Millisecond))
	}

	// CONFIRM from kernel facts. The signal's error value proves nothing about what
	// is running now, so it is not consulted here.
	live, liveOK := identityFromPID(procRoot, newPID)
	if !wantedOK || !liveOK || !identitiesMatch(live, wanted) {
		return fmt.Errorf("%s respawned as pid %d after %s but it is NOT running the newly installed binary (exe identity does not match) — the old binary may still be on PATH ahead of the install",
			serviceUnitName, newPID, time.Since(start).Round(time.Millisecond))
	}
	// The respawn must have come FROM the supervisor. Same parent proves it, and
	// rules out an unrelated process having taken the port.
	if got, ok := persistentSupervisor(procRoot, newPID); !ok || got.PID != sup.PID {
		return fmt.Errorf("%s respawned as pid %d on the new binary, but it was not respawned by the original supervisor (pid %d) — refusing to report a durable restart",
			serviceUnitName, newPID, sup.PID)
	}
	fmt.Fprintf(out, "restarted %s (supervisor respawn, pid %d) onto the new binary in %s\n",
		serviceUnitName, newPID, time.Since(start).Round(time.Millisecond))
	return nil
}

// signalAndAwaitRespawn signals pid and waits a BOUNDED window for a different
// process to be holding the service. Returns the new pid and whether one
// appeared. The wait reuses the identity poll cadence, so tests shrink it the
// same way they shrink waitForIdentity.
func signalAndAwaitRespawn(procRoot string, pid int, sig syscall.Signal) (int, bool) {
	if err := restartHooks.signalPID(pid, sig); err != nil {
		return 0, false
	}
	for i := 0; i < identityPollAttempts; i++ {
		if newPID := discoverLivePID(procRoot); newPID > 0 && newPID != pid {
			return newPID, true
		}
		time.Sleep(identityPollInterval)
	}
	return 0, false
}

// discoverLivePID finds whichever process is holding the service NOW — cgroup
// first, then listening port. Re-run after a respawn rather than reusing the
// pre-cycle pid: the old pid is gone, and the only thing that matters is what
// currently serves.
func discoverLivePID(procRoot string) int {
	if pid := findPIDByCgroup(procRoot, serviceUnitName); pid > 0 {
		return pid
	}
	return findPIDByListenPort(procRoot, servicePort())
}

// reportRestartOutcome renders the terminal message for a restart that WAS
// commanded (user-unit restart, or a respawned system unit): success only when
// exe identity confirms the live process is the newly installed binary (AC1) —
// a restart command's exit code or a changed PID alone is not proof.
func reportRestartOutcome(out io.Writer, via string, pid int, matched bool) {
	switch {
	case pid <= 0:
		fmt.Fprintf(out, "restarted %s (%s) but could not locate its process to confirm the new binary is running\n", serviceUnitName, via)
	case matched:
		fmt.Fprintf(out, "restarted %s (%s, pid %d) onto the new binary\n", serviceUnitName, via, pid)
	default:
		fmt.Fprintf(out, "restarted %s (%s, pid %d) but could not confirm it is running the new binary\n", serviceUnitName, via, pid)
	}
}

// identityPollAttempts/identityPollInterval bound waitForIdentity's poll — vars
// (not consts) so hermetic tests shrink the window instead of paying the real
// ~4.5s cost on every no-match scenario.
var (
	identityPollAttempts = 15
	identityPollInterval = 300 * time.Millisecond
)

// waitForIdentity polls the live process's exe identity for a short window,
// matching the existing systemUnitRespawned poll cadence — a fresh exec can take
// a moment to show up. Returns false when either identity is unavailable, never
// treating "unknown" as a match.
func waitForIdentity(procRoot string, pid int, wanted exeIdentity, wantedOK bool) bool {
	if pid <= 0 || !wantedOK {
		return false
	}
	for i := 0; i < identityPollAttempts; i++ {
		if live, ok := identityFromPID(procRoot, pid); ok && identitiesMatch(live, wanted) {
			return true
		}
		time.Sleep(identityPollInterval)
	}
	return false
}

// signalPID sends sig to pid via os.Process.Signal (not syscall.Kill, which is
// Unix-only and breaks the Windows cross-build). The systemd restart path only runs
// on Linux; this just has to COMPILE on Windows, where Signal returns unsupported.
func signalPID(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

// userUnitActive reports whether the systemd USER unit is active, defaulting the
// user-session env so a headless shell can reach the user bus.
func userUnitActive() bool {
	c := exec.Command("systemctl", "--user", "is-active", serviceUnitName)
	c.Env = userEnv()
	out, _ := c.Output()
	return strings.TrimSpace(string(out)) == "active"
}

// systemUnitMainPID returns the MainPID of the ACTIVE system unit, or 0 when the
// unit is not active. Read-only (`systemctl show` needs no sudo).
func systemUnitMainPID() int {
	act, _ := exec.Command("systemctl", "is-active", serviceUnitName).Output()
	if strings.TrimSpace(string(act)) != "active" {
		return 0
	}
	out, _ := exec.Command("systemctl", "show", "--property=MainPID", "--value", serviceUnitName).Output()
	return parseMainPID(string(out))
}

// parseMainPID extracts a PID from `systemctl show --property=MainPID` output —
// either "1234" (with --value) or "MainPID=1234". Pure/unit-tested. Returns 0 for
// none/PID 0 (inactive) or PID 1 (never our serve).
func parseMainPID(s string) int {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 1 {
		return 0
	}
	return n
}

// systemUnitRestartAlways reports whether the system unit's Restart policy is
// "always" — the precondition for a sudo-free SIGTERM restart to RESPAWN rather
// than stop the service. Read-only (no sudo).
func systemUnitRestartAlways() bool {
	out, _ := exec.Command("systemctl", "show", "--property=Restart", "--value", serviceUnitName).Output()
	return strings.TrimSpace(string(out)) == "always"
}

// systemUnitRespawned confirms the system unit came back on a DIFFERENT MainPID
// after the SIGTERM — proof the supervisor respawned it (Restart=always) rather
// than it being a Restart=on-failure unit that stopped on the clean signal. Returns
// the new PID so the caller can verify exe identity on it (AC1) — a changed PID
// alone is evidence of a respawn, not evidence of WHICH binary it respawned onto.
func systemUnitRespawned(oldPID int) (int, bool) {
	for i := 0; i < 15; i++ {
		time.Sleep(300 * time.Millisecond)
		if np := systemUnitMainPID(); np > 0 && np != oldPID {
			return np, true
		}
	}
	return 0, false
}

// userUnitMainPID returns the MainPID of the ACTIVE user unit, or 0. Mirrors
// systemUnitMainPID but through the user-session systemctl (userEnv()).
func userUnitMainPID() int {
	c := exec.Command("systemctl", "--user", "show", "--property=MainPID", "--value", serviceUnitName)
	c.Env = userEnv()
	out, _ := c.Output()
	return parseMainPID(string(out))
}

// systemUnitStartLimited reports whether the system unit has exhausted systemd's
// restart attempts (StartLimitBurst) and given up — a distinct, nameable failure
// mode (AC4) rather than a silent fold into "did not respawn".
func systemUnitStartLimited() bool {
	out, _ := exec.Command("systemctl", "show", "--property=Result", "--value", serviceUnitName).Output()
	return parseUnitStartLimited(string(out))
}

// parseUnitStartLimited is the pure classifier: `systemctl show --property=Result`
// reports "start-limit-hit" when StartLimitBurst was exhausted. Pure/unit-tested.
func parseUnitStartLimited(result string) bool {
	return strings.TrimSpace(strings.ToLower(result)) == "start-limit-hit"
}

// --- Exe identity (AC1): proof a running process is the newly installed binary,
// independent of a restart command's exit code or a version string that may not
// have changed across a release that did not touch this binary. ---

// exeIdentity is the on-disk identity of an executable: device+inode is the
// authoritative comparison (survives a rename-over-target replace); size is cheap
// corroboration only.
type exeIdentity struct {
	Dev, Ino uint64
	Size     int64
}

// identityFromPath stats path (following symlinks) and extracts dev+inode via the
// platform-specific statIdentity (cmd_update_identity_*.go — dev+inode has no
// portable representation, e.g. Windows' os.FileInfo carries no syscall.Stat_t).
// ok is false when the path cannot be stat'd or the platform cannot report identity.
func identityFromPath(path string) (id exeIdentity, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return exeIdentity{}, false
	}
	dev, ino, ok := statIdentity(info)
	if !ok {
		return exeIdentity{}, false
	}
	return exeIdentity{Dev: dev, Ino: ino, Size: info.Size()}, true
}

// identityFromPID stats procRoot/<pid>/exe directly (NOT a readlink+restat): Linux
// resolves this magic symlink to the process's currently-mapped executable inode
// even after the on-disk path has been renamed away (the exact situation right
// after `satelle update` replaces the binary under a still-running process), so
// this is reliable where readlink's path string ("...(deleted)") is not.
func identityFromPID(procRoot string, pid int) (exeIdentity, bool) {
	if pid <= 0 {
		return exeIdentity{}, false
	}
	return identityFromPath(filepath.Join(procRoot, strconv.Itoa(pid), "exe"))
}

// identitiesMatch compares dev+inode — the same file, however it was reached.
func identitiesMatch(a, b exeIdentity) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino
}

// wantedExeIdentity is the ground truth for "the newly installed binary": the
// ExecStart binary named by whichever unit file is present (user, then system),
// falling back to the CLI install target when no unit file can be read. ok is
// false only when neither source resolves — callers must never treat that as a
// match.
func wantedExeIdentity(procRoot string) (exeIdentity, bool) {
	if content, ok := installedUnitFile(); ok {
		if bin := parseExecStartBinary(content); bin != "" {
			if id, ok := identityFromPath(bin); ok {
				return id, true
			}
		}
	}
	return identityFromPath(installTarget())
}

// parseExecStartBinary extracts the binary path from a unit file's ExecStart=
// line (the first whitespace-delimited token). Pure/unit-tested.
func parseExecStartBinary(unitContent string) string {
	return firstToken(unitDirective(unitContent, "ExecStart"))
}

// unitDirective returns the value of the first `key=` line in a unit file, or ""
// when absent. ONE scanner serves every directive the restart path reads
// (ExecStart, Restart) so the two cannot diverge in whitespace or case handling
// (sty_d45618d5).
func unitDirective(unitContent, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(unitContent, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	return ""
}

// firstToken returns s up to the first space or tab.
func firstToken(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// --- Bus-independent discovery (AC2): find the live service process when
// systemctl cannot reach either the user or system bus. ---

// findPIDByCgroup scans procRoot/<pid>/cgroup for a line naming unitName (e.g.
// ".../app.slice/satelle.service"), bounded so a substring collision (e.g. a unit
// named "notsatelle.service") does not false-match. Returns 0 when not found.
// Bus-independent: cgroup membership is a kernel fact, not a systemctl query.
func findPIDByCgroup(procRoot, unitName string) int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0
	}
	suffix := "/" + unitName
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cgroup"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimRight(line, "\r")
			if line == unitName || strings.HasSuffix(line, suffix) {
				return pid
			}
		}
	}
	return 0
}

// servicePort resolves the configured web-service port, falling back to the
// documented default when global config cannot be loaded.
func servicePort() int {
	port, _ := servicePortResolved()
	return port
}

// servicePortResolved is servicePort plus whether the global config actually
// loaded. Callers that must not fabricate certainty from a fallback port read
// the flag (sty_fb5e6d96); the rest keep using servicePort. One resolution path
// serves both, so no surface can drift onto a different port.
func servicePortResolved() (int, bool) {
	gc, err := config.LoadGlobal()
	if err != nil {
		return config.DefaultWebPort, false
	}
	return gc.Service.ResolvePort(), true
}

// findPIDByListenPort locates the process holding a LISTENing TCP socket on port
// by matching procRoot/net/tcp{,6}'s socket inode to a procRoot/<pid>/fd entry.
// Bus-independent fallback for when cgroup discovery finds nothing (e.g. a
// non-systemd supervisor). Best-effort: returns 0 on any lookup failure.
func findPIDByListenPort(procRoot string, port int) int {
	inode := ""
	for _, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(procRoot, "net", name))
		if err != nil {
			continue
		}
		if found := parseListenInode(string(data), port); found != "" {
			inode = found
			break
		}
	}
	if inode == "" {
		return 0
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0
	}
	want := "socket:[" + inode + "]"
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(procRoot, e.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(procRoot, e.Name(), "fd", fd.Name()))
			if err == nil && link == want {
				return pid
			}
		}
	}
	return 0
}

// parseListenInode finds the socket inode for a LISTEN-state (0A) row in
// /proc/net/tcp{,6} whose local port matches. Pure/unit-tested. Format:
// "  sl  local_address rem_address   st ... inode ...", local_address is
// "hex_ip:hex_port".
func parseListenInode(procNetTCP string, port int) string {
	wantPort := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	lines := strings.Split(procNetTCP, "\n")
	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		local := fields[1]
		st := fields[3]
		i := strings.LastIndexByte(local, ':')
		if i < 0 || !strings.EqualFold(local[i+1:], wantPort) {
			continue
		}
		if !strings.EqualFold(st, "0A") { // TCP_LISTEN
			continue
		}
		return fields[9]
	}
	return ""
}
