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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

const updateRepo = "bobmcallan/satelle"

func init() {
	var check, noRestart bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the installed satelle binary to the latest release",
		Long: `update resolves the latest GitHub release and, if it is newer than the
installed binary, downloads the platform asset, sha256-verifies it, and replaces
the installed binary in place — the same asset/checksum/location scheme as the
curl installer. If the background service is running it is restarted onto the new
binary. --check reports availability without changing anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			target := installTarget()
			latest, err := latestReleaseTag(cmd.Context(), updateRepo)
			if err != nil {
				return fmt.Errorf("resolve latest release: %w", err)
			}
			current := installedVersion(target)
			if !updateAvailable(current, latest) {
				fmt.Fprintf(out, "already up to date (%s)\n", current)
				return nil
			}
			if check {
				fmt.Fprintf(out, "update available: %s → %s  (run `satelle update`)\n", current, latest)
				return nil
			}
			// Don't clobber a from-source build with a release — the developer
			// manages those via `make install`. A custom release source (mirror/CI/
			// test) opts in regardless.
			if msg := selfUpdateBlocked(buildinfo.Resolve().Version, customReleaseSource()); msg != "" {
				return fmt.Errorf("%s", msg)
			}
			fmt.Fprintf(out, "updating %s: %s → %s\n", target, current, latest)
			if err := downloadAndReplace(cmd.Context(), updateRepo, latest, target); err != nil {
				return err
			}
			fmt.Fprintf(out, "installed %s (%s)\n", target, latest)
			if !noRestart {
				restartServiceIfRunning(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report whether an update is available without installing")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart the background service after updating")
	register(cmd)
}

// customReleaseSource reports whether a non-default release source is configured
// (a mirror, CI, or a test fixture) — which opts into self-update regardless of
// the running build.
func customReleaseSource() bool {
	return os.Getenv("SATELLE_RELEASE_API") != "" || os.Getenv("SATELLE_RELEASE_BASE") != ""
}

// selfUpdateBlocked returns a non-empty refusal reason when self-update must not
// proceed: a from-source (non-release) build with the default release source.
// Such a build is managed by the developer, not by `satelle update`.
func selfUpdateBlocked(version string, customSource bool) string {
	if customSource || buildinfo.IsReleaseVersion(version) {
		return ""
	}
	return fmt.Sprintf("from-source build %s — `satelle update` only refreshes released installs; reinstall with `make install` or the curl installer", version)
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
	if out, err := exec.Command(target, "version").Output(); err == nil {
		// "satelle v0.0.6 (commit …)" → "v0.0.6"
		if fields := strings.Fields(string(out)); len(fields) >= 2 {
			return fields[1]
		}
	}
	return buildinfo.Resolve().Version
}

// assetName is the release asset filename for this platform — identical to the
// name install.sh derives (Go's GOOS/GOARCH already match the published amd64/
// arm64 + linux/darwin asset suffixes).
func assetName(tag string) string {
	name := fmt.Sprintf("satelle-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
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

// downloadAndReplace downloads the platform asset for tag from repo's releases,
// verifies its sha256, and atomically replaces target. The download base is the
// GitHub default, overridable via SATELLE_RELEASE_BASE (mirrors, tests).
func downloadAndReplace(ctx context.Context, repo, tag, target string) error {
	base := os.Getenv("SATELLE_RELEASE_BASE")
	if base == "" {
		base = fmt.Sprintf("https://github.com/%s/releases/download", repo)
	}
	return downloadAndReplaceFrom(ctx, base+"/"+tag, assetName(tag), target)
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
	want := strings.TrimSpace(shaLine)
	if i := strings.IndexAny(want, " \t"); i > 0 {
		want = want[:i]
	}
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

// restartServiceIfRunning restarts the systemd user service if it is active so
// it runs the new binary; otherwise it is a no-op.
func restartServiceIfRunning(out io.Writer) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	active, _ := exec.Command("systemctl", "--user", "is-active", serviceUnitName).Output()
	if strings.TrimSpace(string(active)) != "active" {
		return
	}
	if err := exec.Command("systemctl", "--user", "restart", serviceUnitName).Run(); err != nil {
		fmt.Fprintf(out, "restart the service to use the new binary: systemctl --user restart %s\n", serviceUnitName)
		return
	}
	fmt.Fprintf(out, "restarted %s onto the new binary\n", serviceUnitName)
}
