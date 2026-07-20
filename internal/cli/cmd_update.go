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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

const updateRepo = "bobmcallan/satelle"

func init() {
	var check, noRestart, local bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update satelle to the latest release (--local pins it under this repo's .satelle/)",
		Long: `update resolves the latest GitHub release and, if it differs from the
installed binary, downloads the platform asset, sha256-verifies it, and replaces
the installed binary in place — the same asset/checksum/location scheme as the
curl installer. If the background service is running it is restarted onto the new
binary. --check reports availability without changing anything.

--local installs the release into THIS repo's .satelle/satelle instead of the
global install dir; a present .satelle/satelle then takes precedence (satelle
re-execs it) so the repo runs its own pinned binary. --local never restarts the
global service.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			target := installTarget()
			if local {
				target = repoLocalTarget()
			}
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
			fmt.Fprintf(out, "updating %s: %s → %s\n", target, current, latest)
			if err := downloadAndReplace(cmd.Context(), updateRepo, latest, target); err != nil {
				return err
			}
			fmt.Fprintf(out, "installed %s (%s)\n", target, latest)
			// Refresh sibling satelle-serve from its own serve-v* release (sty_19ff03f4).
			if !local {
				serveTarget := filepath.Join(filepath.Dir(target), "satelle-serve")
				if runtime.GOOS == "windows" {
					serveTarget += ".exe"
				}
				serveTag, serr := latestServeReleaseTag(cmd.Context(), updateRepo)
				if serr != nil {
					fmt.Fprintf(out, "satelle-serve update skipped: %v\n", serr)
				} else if err := downloadAndReplaceNamed(cmd.Context(), updateRepo, serveTag, "satelle-serve", serveTarget); err != nil {
					fmt.Fprintf(out, "satelle-serve update skipped: %v\n", err)
				} else {
					fmt.Fprintf(out, "installed %s (%s)\n", serveTarget, serveTag)
				}
			}
			// The global service runs the global binary; a repo-local pin does not
			// drive it, so only restart for a global update.
			if !noRestart && !local {
				restartServiceIfRunning(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report whether an update is available without installing")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart the background service after updating")
	cmd.Flags().BoolVar(&local, "local", false, "install into this repo's .satelle/satelle (a repo-local pin) instead of the global binary")
	register(cmd)
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
	return assetNameFor("satelle", tag)
}

// assetNameFor builds a release asset name for binary (satelle | satelle-serve).
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

// latestServeReleaseTag finds the newest serve-v* tag (sty_19ff03f4). Serve
// releases are published with --latest=false so /releases/latest stays CLI.
func latestServeReleaseTag(ctx context.Context, repo string) (string, error) {
	url := os.Getenv("SATELLE_RELEASE_LIST_API")
	if url == "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", repo)
	}
	body, err := httpGetBytes(ctx, url)
	if err != nil {
		return "", err
	}
	return firstPrefixedTag(body, "serve-v")
}

// firstPrefixedTag returns the first tag_name in a GitHub releases JSON array
// that has the given prefix (newest-first list). Pure for unit tests.
func firstPrefixedTag(body []byte, prefix string) (string, error) {
	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", err
	}
	for _, r := range releases {
		if strings.HasPrefix(r.TagName, prefix) {
			return r.TagName, nil
		}
	}
	return "", fmt.Errorf("no release tag with prefix %q", prefix)
}

// downloadAndReplace downloads the platform asset for tag from repo's releases,
// verifies its sha256, and atomically replaces target. The download base is the
// GitHub default, overridable via SATELLE_RELEASE_BASE (mirrors, tests).
func downloadAndReplace(ctx context.Context, repo, tag, target string) error {
	return downloadAndReplaceNamed(ctx, repo, tag, "satelle", target)
}

// downloadAndReplaceNamed downloads a named binary asset (satelle | satelle-serve).
func downloadAndReplaceNamed(ctx context.Context, repo, tag, binary, target string) error {
	base := os.Getenv("SATELLE_RELEASE_BASE")
	if base == "" {
		base = fmt.Sprintf("https://github.com/%s/releases/download", repo)
	}
	return downloadAndReplaceFrom(ctx, base+"/"+tag, assetNameFor(binary, tag), target)
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

// restartServiceIfRunning restarts the background service onto the new binary,
// SUDO-FREE, whatever supervises it. It tries the systemd USER unit first (with the
// user-session env defaulted, so a headless/non-login shell can reach the bus);
// then, if the user unit is not usable but the SYSTEM unit is active, it restarts
// without sudo by SIGTERM-ing the unit's MainPID — the process runs as the operator
// (User=), and the system unit's Restart=always makes systemd respawn it onto the
// new binary (sty_1ac9f095). Previously this only tried `systemctl --user`, which is
// a SILENT no-op when the user manager is down (a WSL quirk), leaving the live
// service on the old binary. A genuine sudo restart stays the operator's fallback.
func restartServiceIfRunning(out io.Writer) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	// 1) systemd USER unit — sudo-free when the user manager is alive.
	if userUnitActive() {
		if err := runCaptureEnv(userEnv(), "systemctl", "--user", "restart", serviceUnitName); err == nil {
			fmt.Fprintf(out, "restarted %s (user unit) onto the new binary\n", serviceUnitName)
			return
		}
	}
	// 2) SYSTEM unit — sudo-free restart by signalling MainPID; Restart=always respawns.
	if pid := systemUnitMainPID(); pid > 0 {
		// Only signal a unit that will RESPAWN — a clean SIGTERM to a Restart=on-failure
		// unit would STOP the service, not reload it. Never leave a good release with a
		// dead service; guide the operator to a persistent supervisor instead.
		if !systemUnitRestartAlways() {
			fmt.Fprintf(out, "system unit %s is not Restart=always — a signal would stop it, not respawn. Upgrade it with `satelle service install --system`, or restart manually: sudo systemctl restart %s\n", serviceUnitName, serviceUnitName)
			return
		}
		if err := signalTerm(pid); err == nil {
			if systemUnitRespawned(pid) {
				fmt.Fprintf(out, "restarted %s (system unit, was pid %d) onto the new binary\n", serviceUnitName, pid)
			} else {
				fmt.Fprintf(out, "signalled %s (pid %d) but it did not respawn — restart manually: sudo systemctl restart %s\n", serviceUnitName, pid, serviceUnitName)
			}
			return
		}
	}
	// 3) No reachable supervisor — tell the operator how to reload the new binary.
	fmt.Fprintf(out, "binary updated, but no restartable service was found — reload it with `sudo systemctl restart %s` (system) or `systemctl --user restart %s` (user)\n", serviceUnitName, serviceUnitName)
}

// signalTerm sends SIGTERM to pid via os.Process.Signal (not syscall.Kill, which is
// Unix-only and breaks the Windows cross-build). The systemd restart path only runs
// on Linux; this just has to COMPILE on Windows, where Signal returns unsupported.
func signalTerm(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
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
// than it being a Restart=on-failure unit that stopped on the clean signal.
func systemUnitRespawned(oldPID int) bool {
	for i := 0; i < 15; i++ {
		time.Sleep(300 * time.Millisecond)
		if np := systemUnitMainPID(); np > 0 && np != oldPID {
			return true
		}
	}
	return false
}
