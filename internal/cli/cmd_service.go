// `satelle service` — install/manage the background web service so the project
// page stays up without an open terminal. On Linux/WSL it manages a systemd
// unit; the global config (~/.satelle/config.toml) holds the port/addr/repo so
// they survive reinstalls and are editable. Native Windows has no systemd, so
// install there prints Task Scheduler guidance instead.

package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"

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
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start the background web service (systemd user unit)",
		Long: `install resolves the service settings (flags > ~/.satelle/config.toml >
defaults), saves them to the global config, and installs a systemd user service
that runs 'satelle serve' for the chosen repo — so the project page stays up
across terminals and WSL restarts, reachable from a Windows browser.

Change the port later by editing ~/.satelle/config.toml (or passing --port) and
re-running 'satelle service install'. On native Windows (no systemd) install
prints Task Scheduler guidance instead.`,
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

			// Platform branch.
			if runtime.GOOS == "windows" {
				printWindowsGuidance(out, bin, resolvedRepo, rAddr, rPort)
				return nil
			}
			if _, lerr := exec.LookPath("systemctl"); lerr != nil {
				printNoSystemdGuidance(out, unit)
				return nil
			}
			return installUserUnit(out, unit, rPort, rAddr)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "service port (default 8787 or saved global config)")
	cmd.Flags().StringVar(&addr, "addr", "", "bind address (default 0.0.0.0 — reachable from Windows)")
	cmd.Flags().StringVar(&repo, "repo", "", "repo to serve (default: current directory or saved config)")
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
			_ = runQuiet("systemctl", "--user", "disable", "--now", serviceUnitName)
			unitPath := userUnitPath()
			if err := os.Remove(unitPath); err == nil {
				fmt.Fprintf(out, "removed %s\n", unitPath)
			}
			_ = runQuiet("systemctl", "--user", "daemon-reload")
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
			active, _ := exec.Command("systemctl", "--user", "is-active", serviceUnitName).Output()
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

// systemdUnit renders the unit file content for the service. Pure (testable):
// the ExecStart bakes in the resolved addr/port and WorkingDirectory selects
// the served repo, so the running service needs no config lookup of its own.
func systemdUnit(binPath, repo, addr string, port int) string {
	return fmt.Sprintf(`[Unit]
Description=satelle web server (project page)
After=network.target

[Service]
ExecStart=%s serve --addr %s --port %d
WorkingDirectory=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, binPath, addr, port, repo)
}

// installUserUnit writes the unit under the user systemd dir and enables it,
// enabling linger so it survives logout and starts on (WSL) boot. systemctl
// failures are reported with the manual equivalent rather than aborting.
func installUserUnit(out io.Writer, unit string, port int, addr string) error {
	unitPath := userUnitPath()
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("service: mkdir %s: %w", filepath.Dir(unitPath), err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("service: write %s: %w", unitPath, err)
	}
	fmt.Fprintf(out, "unit:   %s\n", unitPath)

	steps := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", serviceUnitName},
	}
	if u, err := user.Current(); err == nil {
		// Linger lets the user service run without an active login + start on boot.
		steps = append(steps, []string{"loginctl", "enable-linger", u.Username})
	}
	failed := false
	for _, s := range steps {
		if err := runQuiet(s[0], s[1:]...); err != nil {
			failed = true
			fmt.Fprintf(out, "  ! %s failed: %v\n", joinArgs(s), err)
		}
	}
	if failed {
		fmt.Fprintln(out, "\nAutomatic enable hit an error (common if the user systemd manager")
		fmt.Fprintln(out, "isn't running). Finish manually:")
		for _, s := range steps {
			fmt.Fprintf(out, "  %s\n", joinArgs(s))
		}
		fmt.Fprintln(out, "Or install a system unit (always-on while WSL runs):")
		fmt.Fprintf(out, "  sudo cp %s /etc/systemd/system/%s\n", unitPath, serviceUnitName)
		fmt.Fprintf(out, "  sudo systemctl enable --now %s\n", serviceUnitName)
		return nil
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

// runQuiet runs a command, discarding output; returns the error (if any).
func runQuiet(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func joinArgs(s []string) string {
	out := s[0]
	for _, a := range s[1:] {
		out += " " + a
	}
	return out
}
