// `satelle service` — install/manage the background web service so the project
// page stays up without an open terminal. On Linux/WSL it manages a systemd
// unit; the global config (~/.satelle/config.toml) holds the port/addr/repo so
// they survive reinstalls and are editable. Native Windows has no systemd, so
// install there prints Task Scheduler guidance instead.

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
)

const serviceUnitName = "satelle.service"

func init() {
	svc := &cobra.Command{
		Use:   "service",
		Short: "Manage the background web service (always-on project page)",
	}
	svc.AddCommand(serviceInstallCmd(), serviceUninstallCmd(), serviceStatusCmd())
	register(svc)
}

func serviceInstallCmd() *cobra.Command {
	var port int
	var addr, repo string
	var system bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start the background web service (systemd user unit)",
		Long: `install resolves the service settings (flags > ~/.satelle/config.toml >
defaults), saves them to the global config, and installs a systemd user service
that runs 'satelle serve' for the chosen repo — so the project page stays up
across terminals and WSL restarts, reachable from a Windows browser.

Re-running after 'make install' restarts the unit so the live process loads the
new binary (release dogfood). systemctl --user calls are run with the systemd
user-session env defaulted (XDG_RUNTIME_DIR / DBUS_SESSION_BUS_ADDRESS), so a
headless / non-login agent shell with a running user manager still succeeds.

When the user bus is genuinely unreachable (no running user manager), the binary
still installed fine — install says so and points at the persistent fallback
rather than reporting a failed binary. For a supervisor that SURVIVES session
loss, use --system: it installs a system unit (WantedBy=multi-user.target,
running as you) via sudo. This is also the persistent supervisor for the
connected-projects fleet. On native Windows, install prints Task Scheduler
guidance and exits non-zero until the service is managed by a real install path.

Change the port later by editing ~/.satelle/config.toml (or passing --port) and
re-running 'satelle service install'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			bin, err := resolveSelfPath()
			if err != nil {
				return err
			}

			// Resolve settings: flags override the saved global config; repo
			// defaults to the saved value, else the current directory.
			gc, _ := config.LoadGlobal()
			if cmd.Flags().Changed("port") {
				gc.Service.Port = port
			}
			if cmd.Flags().Changed("addr") {
				gc.Service.Addr = addr
			}
			resolvedRepo := repo
			if resolvedRepo == "" {
				resolvedRepo = gc.Service.Repo
			}
			if resolvedRepo == "" {
				if wd, werr := os.Getwd(); werr == nil {
					resolvedRepo = wd
				}
			}
			if abs, aerr := filepath.Abs(resolvedRepo); aerr == nil {
				resolvedRepo = abs
			}
			gc.Service.Repo = resolvedRepo
			if err := config.SaveGlobal(gc); err != nil {
				return err
			}
			rPort, rAddr := gc.Service.ResolvePort(), gc.Service.ResolveAddr()
			fmt.Fprintf(out, "config: %s (port %d, addr %s, repo %s)\n",
				config.GlobalConfigPath(), rPort, rAddr, resolvedRepo)

			unit := systemdUnit(bin, resolvedRepo, rAddr, rPort)

			// Platform branch. Install must actually start (or restart) the service
			// for dogfood — soft success with printed guidance is a failed install
			// (release treats local install failure as release failure).
			if runtime.GOOS == "windows" {
				printWindowsGuidance(out, bin, resolvedRepo, rAddr, rPort)
				return fmt.Errorf("service: native Windows has no systemd install path — configure Task Scheduler as printed, then re-run when the service is managed")
			}
			if _, lerr := exec.LookPath("systemctl"); lerr != nil {
				printNoSystemdGuidance(out, unit)
				return fmt.Errorf("service: systemctl not found — enable systemd or install systemctl, then re-run satelle service install")
			}
			// --system installs a PERSISTENT system unit (survives session loss) via
			// sudo — the honest path when the user bus can't be brought up.
			if system {
				return installSystemUnit(out, systemSystemdUnit(bin, resolvedRepo, rAddr, rPort, currentUsername()), rPort)
			}
			return installUserUnit(out, unit, rPort, rAddr)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "service port (default 8787 or saved global config)")
	cmd.Flags().StringVar(&addr, "addr", "", "bind address (default 0.0.0.0 — reachable from Windows)")
	cmd.Flags().StringVar(&repo, "repo", "", "repo to serve (default: current directory or saved config)")
	cmd.Flags().BoolVar(&system, "system", false, "install a persistent system unit via sudo (survives session loss; needs sudo)")
	return cmd
}

func serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the background web service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if runtime.GOOS == "windows" {
				fmt.Fprintln(out, "On Windows, remove the satelle task from Task Scheduler.")
				return nil
			}
			if _, err := exec.LookPath("systemctl"); err != nil {
				fmt.Fprintln(out, "systemctl not found — nothing to uninstall.")
				return nil
			}
			env := userEnv()
			_ = runQuietEnv(env, "systemctl", "--user", "disable", "--now", serviceUnitName)
			unitPath := userUnitPath()
			if err := os.Remove(unitPath); err == nil {
				fmt.Fprintf(out, "removed %s\n", unitPath)
			}
			_ = runQuietEnv(env, "systemctl", "--user", "daemon-reload")
			fmt.Fprintln(out, "service uninstalled.")
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the background web service status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if runtime.GOOS == "windows" {
				fmt.Fprintln(out, "On Windows, check the satelle task in Task Scheduler.")
				return nil
			}
			if _, err := exec.LookPath("systemctl"); err != nil {
				fmt.Fprintln(out, "systemctl not found.")
				return nil
			}
			isActive := exec.Command("systemctl", "--user", "is-active", serviceUnitName)
			isActive.Env = userEnv()
			active, _ := isActive.Output()
			state := "inactive (not installed or stopped)"
			if s := string(active); len(s) > 0 {
				state = s[:len(s)-1] // trim newline
			}
			gc, _ := config.LoadGlobal()
			fmt.Fprintf(out, "service: %s\n", state)
			fmt.Fprintf(out, "config:  %s (port %d, addr %s, repo %s)\n",
				config.GlobalConfigPath(), gc.Service.ResolvePort(), gc.Service.ResolveAddr(), gc.Service.Repo)
			fmt.Fprintf(out, "url:     http://localhost:%d\n", gc.Service.ResolvePort())
			return nil
		},
	}
}

// renderUnit renders the unit file content for the service. Pure (testable): the
// ExecStart bakes in the resolved addr/port and WorkingDirectory selects the launch
// repo. `serve` is always adaptive — it shows the connected-projects landing at /
// and serves every registered project under /<slug>/ — so no extra flag is needed.
// wantedBy selects the install target: default.target for a per-user unit,
// multi-user.target for a system unit that runs with no login. A system unit adds
// User=/Group= so it runs as the operator (reaching ~/.satelle and the repo), not root.
func renderUnit(binPath, repo, addr string, port int, wantedBy, runAsUser string) string {
	userLines := ""
	if runAsUser != "" {
		userLines = fmt.Sprintf("User=%s\nGroup=%s\n", runAsUser, runAsUser)
	}
	return fmt.Sprintf(`[Unit]
Description=satelle web server (project page)
After=network.target

[Service]
ExecStart=%s serve --addr %s --port %d
WorkingDirectory=%s
%sRestart=on-failure
RestartSec=2

[Install]
WantedBy=%s
`, binPath, addr, port, repo, userLines, wantedBy)
}

// systemdUnit renders the per-user unit (WantedBy=default.target, runs as the
// logged-in user via the user manager).
func systemdUnit(binPath, repo, addr string, port int) string {
	return renderUnit(binPath, repo, addr, port, "default.target", "")
}

// systemSystemdUnit renders the persistent SYSTEM unit (WantedBy=multi-user.target)
// that survives session loss, running as runAsUser so it still reaches the user's
// config and repo. This is the fleet supervisor when the user bus is unreachable.
func systemSystemdUnit(binPath, repo, addr string, port int, runAsUser string) string {
	return renderUnit(binPath, repo, addr, port, "multi-user.target", runAsUser)
}

// installUserUnit writes the unit under the user systemd dir and enables it,
// enabling linger so it survives logout and starts on (WSL) boot. systemctl
// failures return an error (non-zero exit) — a soft "printed guidance" success
// left a stale serve process after make install (footer version drift).
func installUserUnit(out io.Writer, unit string, port int, addr string) error {
	unitPath := userUnitPath()
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("service: mkdir %s: %w", filepath.Dir(unitPath), err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("service: write %s: %w", unitPath, err)
	}
	fmt.Fprintf(out, "unit:   %s\n", unitPath)

	// enable (persist) then restart — restart starts the unit if stopped and
	// reloads a NEW binary if it is already running, so re-running install after a
	// rebuild actually redeploys (enable --now alone is a no-op on a live unit).
	steps := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", serviceUnitName},
		{"systemctl", "--user", "restart", serviceUnitName},
	}
	if u, err := user.Current(); err == nil {
		// Linger lets the user service run without an active login + start on boot.
		steps = append(steps, []string{"loginctl", "enable-linger", u.Username})
	}
	env := userEnv() // default the systemd user-session env for a headless shell
	var failed []string
	for _, s := range steps {
		runEnv := env
		if s[0] != "systemctl" {
			runEnv = nil // loginctl talks to system logind — inherit the plain env
		}
		if err := runCaptureEnv(runEnv, s[0], s[1:]...); err != nil {
			failed = append(failed, joinArgs(s)+": "+err.Error())
			fmt.Fprintf(out, "  ! %s failed: %v\n", joinArgs(s), err)
		}
	}
	if len(failed) > 0 {
		// The BINARY installed fine (make install succeeded, the user unit is written);
		// only attaching a persistent supervisor failed. When the cause is an
		// unreachable user bus, lead with the --system remediation and label this as a
		// supervisor-attach gap, not a failed binary (sty_00dadc91, aligns with the
		// mechanism-agnostic release skill sty_dfc73ced).
		fmt.Fprintf(out, "\nBinary installed OK; the user unit is written to %s.\n", unitPath)
		if busUnreachable(failed) {
			fmt.Fprintln(out, "The systemd --user bus is unreachable (no running user manager in this")
			fmt.Fprintln(out, "headless/non-login session). For a PERSISTENT supervisor that survives")
			fmt.Fprintln(out, "session loss, install a system unit:")
			fmt.Fprintln(out, "  satelle service install --system")
			fmt.Fprintln(out, "(or do it by hand:)")
		} else {
			fmt.Fprintln(out, "\nAutomatic enable hit an error. Finish manually, then re-run service install:")
			for _, s := range steps {
				fmt.Fprintf(out, "  %s\n", joinArgs(s))
			}
			fmt.Fprintln(out, "Or install a persistent system unit (survives session loss):")
			fmt.Fprintln(out, "  satelle service install --system")
			fmt.Fprintln(out, "(or by hand:)")
		}
		fmt.Fprintf(out, "  sudo cp %s /etc/systemd/system/%s\n", unitPath, serviceUnitName)
		fmt.Fprintf(out, "  sudo systemctl enable --now %s\n", serviceUnitName)
		if busUnreachable(failed) {
			return fmt.Errorf("service: binary installed OK, but the systemd --user bus is unreachable — run \"satelle service install --system\" for a persistent unit (user-manager detail: %s)",
				strings.Join(failed, "; "))
		}
		return fmt.Errorf("service: systemctl --user could not enable/restart %s: %s",
			serviceUnitName, strings.Join(failed, "; "))
	}
	fmt.Fprintf(out, "\nservice running → http://localhost:%d\n", port)
	if addr == "0.0.0.0" {
		fmt.Fprintln(out, "(reachable from a Windows browser at the same URL when satelle runs in WSL)")
	}
	return nil
}

func printWindowsGuidance(out io.Writer, bin, repo, addr string, port int) {
	fmt.Fprintln(out, "\nNative Windows has no systemd. To run the service on login, create a")
	fmt.Fprintln(out, "Task Scheduler task (Trigger: At log on; Action: Start a program):")
	fmt.Fprintf(out, "  Program:   %s\n", bin)
	fmt.Fprintf(out, "  Arguments: serve --addr %s --port %d\n", addr, port)
	fmt.Fprintf(out, "  Start in:  %s\n", repo)
	fmt.Fprintf(out, "Then browse http://localhost:%d\n", port)
}

func printNoSystemdGuidance(out io.Writer, unit string) {
	fmt.Fprintln(out, "\nsystemctl not found (systemd not enabled in this environment).")
	fmt.Fprintln(out, "Enable systemd in WSL (/etc/wsl.conf → [boot] systemd=true, then `wsl --shutdown`),")
	fmt.Fprintln(out, "or run the server in the background yourself. The unit to install once systemd is on:")
	fmt.Fprintln(out, "\n"+unit)
}

// resolveSelfPath returns the absolute path of the running satelle binary, used
// in the unit's ExecStart. Re-run install after moving the binary.
func resolveSelfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("service: resolve binary path: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return exe, nil
}

func userUnitPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "systemd", "user", serviceUnitName)
}

// systemUnitPath is where a persistent SYSTEM unit lives (needs root to write).
func systemUnitPath() string {
	return filepath.Join("/etc/systemd/system", serviceUnitName)
}

// currentUsername returns the login name for a system unit's User=, falling back
// to the numeric uid when the name can't be resolved.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return currentUID()
}

// busUnreachable reports whether any systemctl --user failure was a
// bus-connection error (no running user manager), as opposed to a genuine
// enable/restart fault. Pure (unit-tested); case-insensitive.
func busUnreachable(failures []string) bool {
	for _, f := range failures {
		l := strings.ToLower(f)
		if strings.Contains(l, "connect to bus") ||
			strings.Contains(l, "user scope bus") ||
			strings.Contains(l, "no such file or directory") && strings.Contains(l, "bus") {
			return true
		}
	}
	return false
}

// systemInstallSteps is the pure command plan for a persistent system-unit install
// (unit-tested): copy the rendered unit into place, reload, enable+start. Each runs
// under sudo — a system path needs root; it needs neither --user nor linger.
func systemInstallSteps(srcUnit, destUnit string) [][]string {
	return [][]string{
		{"sudo", "install", "-m", "0644", srcUnit, destUnit},
		{"sudo", "systemctl", "daemon-reload"},
		{"sudo", "systemctl", "enable", "--now", serviceUnitName},
	}
}

// installSystemUnit writes the rendered system unit to a temp file and runs the
// sudo step plan to install a PERSISTENT supervisor that survives session loss.
// sudo is REQUIRED and never auto-elevated silently: if sudo is absent the manual
// commands are printed and a distinct error returned. sudo may prompt for a
// password (steps inherit the tty). A step failure prints the manual fallback.
func installSystemUnit(out io.Writer, unit string, port int) error {
	dest := systemUnitPath()
	if _, err := exec.LookPath("sudo"); err != nil {
		printManualSystemInstall(out, unit, dest)
		return fmt.Errorf("service: --system needs sudo, which was not found — write %s as root using the printed unit, then `systemctl enable --now %s`", dest, serviceUnitName)
	}
	tmp, err := os.CreateTemp("", "satelle-service-*.service")
	if err != nil {
		return fmt.Errorf("service: create temp unit: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, werr := tmp.WriteString(unit); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("service: write temp unit: %w", werr)
	}
	_ = tmp.Close()

	fmt.Fprintf(out, "installing persistent system unit %s (sudo may prompt)…\n", dest)
	var failed []string
	for _, s := range systemInstallSteps(tmpPath, dest) {
		if err := runQuiet(s[0], s[1:]...); err != nil { // inherit tty so sudo can prompt
			failed = append(failed, joinArgs(s)+": "+err.Error())
			fmt.Fprintf(out, "  ! %s failed: %v\n", joinArgs(s), err)
		}
	}
	if len(failed) > 0 {
		printManualSystemInstall(out, unit, dest)
		return fmt.Errorf("service: system-unit install failed: %s", strings.Join(failed, "; "))
	}
	fmt.Fprintf(out, "\npersistent system service running → http://localhost:%d\n", port)
	fmt.Fprintln(out, "(survives logout/session loss; manage with `sudo systemctl status/restart "+serviceUnitName+"`)")
	return nil
}

// printManualSystemInstall prints the copy-pasteable root steps to install the
// system unit by hand when sudo is unavailable or a step failed.
func printManualSystemInstall(out io.Writer, unit, dest string) {
	fmt.Fprintln(out, "\nInstall the persistent system unit by hand (as root):")
	fmt.Fprintf(out, "  sudo tee %s >/dev/null <<'UNIT'\n%sUNIT\n", dest, unit)
	fmt.Fprintln(out, "  sudo systemctl daemon-reload")
	fmt.Fprintf(out, "  sudo systemctl enable --now %s\n", serviceUnitName)
}

// userSystemctlEnv defaults the systemd user-session env vars when they are absent,
// so `systemctl --user` can reach a running user manager from a NON-LOGIN shell (a
// headless agent session) that never inherited them — the "Failed to connect to bus"
// breakage. Existing values are preserved. Pure over its inputs (unit-tested).
func userSystemctlEnv(env []string, uid string) []string {
	has := func(key string) bool {
		p := key + "="
		for _, e := range env {
			if strings.HasPrefix(e, p) {
				return true
			}
		}
		return false
	}
	out := append([]string(nil), env...)
	runtimeDir := "/run/user/" + uid
	if !has("XDG_RUNTIME_DIR") {
		out = append(out, "XDG_RUNTIME_DIR="+runtimeDir)
	}
	if !has("DBUS_SESSION_BUS_ADDRESS") {
		out = append(out, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtimeDir+"/bus")
	}
	return out
}

// currentUID returns the numeric uid as a string (user.Current, falling back to
// os.Getuid), for the /run/user/<uid> defaults.
func currentUID() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return u.Uid
	}
	return fmt.Sprintf("%d", os.Getuid())
}

// userEnv is the process env with the systemd user-session defaults applied.
func userEnv() []string {
	return userSystemctlEnv(os.Environ(), currentUID())
}

// runQuiet runs a command, discarding output; returns the error (if any).
func runQuiet(name string, args ...string) error {
	return runQuietEnv(nil, name, args...)
}

// runQuietEnv runs a command with an explicit env (nil = inherit), discarding
// output. `systemctl --user` calls pass userEnv() so a headless shell reaches the
// user bus; sudo/system calls pass nil so they inherit the tty for a password prompt.
func runQuietEnv(env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	if env != nil {
		c.Env = env
	}
	return c.Run()
}

// runCaptureEnv runs a command with env (nil = inherit) and folds its STDERR into
// the returned error, so a diagnostic like "Failed to connect to bus" survives for
// the caller to classify — runQuiet discards it, which would make busUnreachable
// dead code (sty_00dadc91). Not used for sudo (it prompts on the tty, not stderr).
func runCaptureEnv(env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	if env != nil {
		c.Env = env
	}
	var errb bytes.Buffer
	c.Stderr = &errb
	err := c.Run()
	if err != nil {
		if m := strings.TrimSpace(errb.String()); m != "" {
			return fmt.Errorf("%s", m)
		}
	}
	return err
}

func joinArgs(s []string) string {
	out := s[0]
	for _, a := range s[1:] {
		out += " " + a
	}
	return out
}
