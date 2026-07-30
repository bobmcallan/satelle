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
	"github.com/bobmcallan/satelle/internal/config"
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
			cliUpdated := false
			if !updateAvailable(current, latest) {
				fmt.Fprintf(out, "CLI already up to date (%s)\n", current)
			} else if check {
				fmt.Fprintf(out, "update available: %s → %s  (run `satelle update`)\n", current, latest)
				// still report serve availability below when not --check-only for CLI
			} else {
				fmt.Fprintf(out, "updating %s: %s → %s\n", target, current, latest)
				if err := downloadAndReplace(cmd.Context(), updateRepo, latest, target); err != nil {
					return err
				}
				fmt.Fprintf(out, "installed %s (%s)\n", target, latest)
				cliUpdated = true
			}
			if check {
				// Surface serve channel too when independent (sty_19ff03f4).
				if !local {
					if st, serr := latestServeReleaseTag(cmd.Context(), updateRepo); serr == nil {
						fmt.Fprintf(out, "latest serve release: %s\n", st)
					}
				}
				return nil
			}
			// Always refresh sibling satelle-serve from serve-v* even when CLI was current.
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
					cliUpdated = true // restart so serve process can pick up sibling if re-exec path
				}
			}
			// The global service runs the global binary; a repo-local pin does not
			// drive it, so only restart for a global update.
			if cliUpdated && !noRestart && !local {
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
func restartServiceIfRunning(out io.Writer) {
	restartServiceIfRunningRoot(out, "/proc")
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
	signalTerm           func(int) error
	systemUnitRespawned  func(int) (int, bool)
	systemUnitStartLtd   func() bool
}{
	lookSystemctl:        func() error { _, err := exec.LookPath("systemctl"); return err },
	userUnitActive:       userUnitActive,
	userUnitMainPID:      userUnitMainPID,
	restartUserUnit:      func() error { return runCaptureEnv(userEnv(), "systemctl", "--user", "restart", serviceUnitName) },
	systemUnitMainPID:    systemUnitMainPID,
	systemUnitRestartAlw: systemUnitRestartAlways,
	signalTerm:           signalTerm,
	systemUnitRespawned:  systemUnitRespawned,
	systemUnitStartLtd:   systemUnitStartLimited,
}

// restartServiceIfRunningRoot is the injectable core: procRoot lets hermetic tests
// substitute a fake /proc tree (AC6) without touching the real system, and every
// systemctl-touching step routes through restartHooks for the same reason.
func restartServiceIfRunningRoot(out io.Writer, procRoot string) {
	if runtime.GOOS != "linux" {
		return // cgroup/port discovery and /proc identity are Linux-only (WSL is the story context)
	}
	if restartHooks.lookSystemctl() != nil {
		return
	}
	wanted, wantedOK := wantedExeIdentity(procRoot)

	// 1) systemd USER unit — sudo-free when the user manager is alive. Verify by
	// identity, not by the restart command's exit code alone.
	if restartHooks.userUnitActive() {
		if err := restartHooks.restartUserUnit(); err == nil {
			pid := restartHooks.userUnitMainPID()
			reportRestartOutcome(out, "user unit", pid, waitForIdentity(procRoot, pid, wanted, wantedOK))
			return
		}
	}
	// 2) SYSTEM unit — sudo-free restart by signalling MainPID; Restart=always respawns.
	if pid := restartHooks.systemUnitMainPID(); pid > 0 {
		// Only signal a unit that will RESPAWN — a clean SIGTERM to a Restart=on-failure
		// unit would STOP the service, not reload it. Never leave a good release with a
		// dead service; guide the operator to a persistent supervisor instead.
		if !restartHooks.systemUnitRestartAlw() {
			fmt.Fprintf(out, "system unit %s is not Restart=always — a signal would stop it, not respawn. Upgrade it with `satelle service install --system`, or restart manually: sudo systemctl restart %s\n", serviceUnitName, serviceUnitName)
			return
		}
		if err := restartHooks.signalTerm(pid); err == nil {
			if newPID, respawned := restartHooks.systemUnitRespawned(pid); respawned {
				reportRestartOutcome(out, "system unit", newPID, waitForIdentity(procRoot, newPID, wanted, wantedOK))
				return
			}
			if restartHooks.systemUnitStartLtd() {
				fmt.Fprintf(out, "%s is start-limited (systemd exhausted its restart attempts and gave up) — run: systemctl reset-failed %s && sudo systemctl restart %s\n", serviceUnitName, serviceUnitName, serviceUnitName)
				return
			}
			fmt.Fprintf(out, "signalled %s (pid %d) but it did not respawn — restart manually: sudo systemctl restart %s\n", serviceUnitName, pid, serviceUnitName)
			return
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
		return
	}
	live, liveOK := identityFromPID(procRoot, livePID)
	if wantedOK && liveOK && identitiesMatch(live, wanted) {
		fmt.Fprintf(out, "%s (pid %d) is already running the new binary — no restart needed\n", serviceUnitName, livePID)
		return
	}
	// A stale (or unconfirmed) process is running, but its supervisor cannot be
	// commanded from here. Do NOT signal it: a bus-unreachable unit discovered this
	// way is very likely the USER unit (Restart=on-failure) — a clean SIGTERM would
	// STOP it, not reload it (the exact mistake this story was raised to prevent).
	fmt.Fprintf(out, "%s is running (pid %d) but its supervisor is unreachable from here (systemctl could not reach the user/system bus) — it may still be on the OLD binary. Restart it once the supervisor is reachable:\n", serviceUnitName, livePID)
	fmt.Fprintf(out, "  systemctl --user restart %s   (if it is a user unit)\n", serviceUnitName)
	fmt.Fprintf(out, "  sudo systemctl restart %s     (if it is a system unit)\n", serviceUnitName)
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
	for _, unitPath := range []string{userUnitPath(), systemUnitPath()} {
		content, err := os.ReadFile(unitPath)
		if err != nil {
			continue
		}
		if bin := parseExecStartBinary(string(content)); bin != "" {
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
	for _, line := range strings.Split(unitContent, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			rest = rest[:i]
		}
		return rest
	}
	return ""
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
