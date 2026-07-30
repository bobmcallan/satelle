package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestSystemdUnitContent(t *testing.T) {
	unit := systemdUnit("/usr/local/bin/satelle-serve", "/home/u/repo", "0.0.0.0", 8787)
	for _, want := range []string{
		"Description=satelle web server",
		"ExecStart=/usr/local/bin/satelle-serve --addr 0.0.0.0 --port 8787",
		"WorkingDirectory=", // $HOME preferred; not a single repo (sty_dbdadfa0)
		"Restart=always",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "WorkingDirectory=/home/u/repo") {
		t.Errorf("unit must not pin per-repo WorkingDirectory:\n%s", unit)
	}
}

func TestJoinArgs(t *testing.T) {
	got := joinArgs([]string{"systemctl", "--user", "enable", "--now", "satelle.service"})
	if got != "systemctl --user enable --now satelle.service" {
		t.Errorf("joinArgs = %q", got)
	}
}

// TestUserSystemctlEnv: the systemd user-session env is defaulted when absent (so
// a headless shell reaches the user bus) and preserved when already set (sty_00dadc91).
func TestUserSystemctlEnv(t *testing.T) {
	// (a) empty env → both defaults for the given uid.
	got := userSystemctlEnv(nil, "1000")
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("defaulted env missing %q:\n%s", want, joined)
		}
	}
	// (b) existing values are preserved, not duplicated or overwritten.
	pre := []string{"XDG_RUNTIME_DIR=/custom/run", "DBUS_SESSION_BUS_ADDRESS=unix:path=/custom/bus", "PATH=/bin"}
	out := userSystemctlEnv(pre, "1000")
	if len(out) != len(pre) {
		t.Errorf("preset env should be unchanged; got %d entries, want %d:\n%v", len(out), len(pre), out)
	}
	j := strings.Join(out, "\n")
	if strings.Contains(j, "/run/user/1000") {
		t.Errorf("preset env should NOT be overwritten with the /run/user default:\n%s", j)
	}
}

// TestSystemSystemdUnit: the persistent system unit targets multi-user.target and
// runs as the given user, with the correct ExecStart (sty_00dadc91). WorkingDirectory
// is $HOME (push-fed serve needs no per-repo cwd; sty_dbdadfa0 / sty_455f0d6e).
func TestSystemSystemdUnit(t *testing.T) {
	unit := systemSystemdUnit("/usr/local/bin/satelle-serve", "/home/u/repo", "0.0.0.0", 8787, "bobmc")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/satelle-serve --addr 0.0.0.0 --port 8787",
		"WorkingDirectory=", // non-empty home or repo fallback
		"User=bobmc",
		"Group=bobmc",
		"WantedBy=multi-user.target",
		"Restart=always", // sudo-free restart: a clean SIGTERM must respawn, not stop (sty_1ac9f095)
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("system unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "WorkingDirectory=/home/u/repo") {
		t.Errorf("system unit must not pin a single repo WorkingDirectory (push-fed serve):\n%s", unit)
	}
	if strings.Contains(unit, "Restart=on-failure") {
		t.Errorf("system unit must be Restart=always (a clean signal respawns), not on-failure:\n%s", unit)
	}
	// The per-user unit must NOT carry User=/multi-user.target, and is Restart=always
	// so `satelle update`'s bus-free cycle respawns on a GRACEFUL stop (sty_f20f3f3b).
	userUnit := systemdUnit("/usr/local/bin/satelle", "/home/u/repo", "0.0.0.0", 8787)
	if strings.Contains(userUnit, "User=") || strings.Contains(userUnit, "multi-user.target") {
		t.Errorf("per-user unit should stay default.target with no User=:\n%s", userUnit)
	}
	if !strings.Contains(userUnit, "Restart=always") {
		t.Errorf("per-user unit must be Restart=always so a graceful stop respawns:\n%s", userUnit)
	}
}

// TestSystemInstallSteps pins the exact sudo command plan for a system-unit install.
func TestSystemInstallSteps(t *testing.T) {
	steps := systemInstallSteps("/tmp/x.service", "/etc/systemd/system/satelle.service")
	if len(steps) != 4 {
		t.Fatalf("want 4 steps, got %d: %v", len(steps), steps)
	}
	want := []string{
		"sudo install -m 0644 /tmp/x.service /etc/systemd/system/satelle.service",
		"sudo systemctl daemon-reload",
		"sudo systemctl enable satelle.service",  // persist across boot
		"sudo systemctl restart satelle.service", // load the new binary NOW (not enable --now, which no-ops a running unit)
	}
	for i, w := range want {
		if got := joinArgs(steps[i]); got != w {
			t.Errorf("step %d = %q, want %q", i, got, w)
		}
	}
}

// TestRunCaptureEnvFoldsStderr guards the regression that made busUnreachable dead
// code: the runner must fold a command's STDERR into the error (runQuiet discards
// it), so a "Failed to connect to bus" diagnostic survives for classification.
func TestRunCaptureEnvFoldsStderr(t *testing.T) {
	err := runCaptureEnv(nil, "sh", "-c", "echo 'Failed to connect to bus' >&2; exit 1")
	if err == nil {
		t.Fatal("want an error from a failing command")
	}
	if !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Errorf("stderr not folded into error: %v", err)
	}
	if err := runCaptureEnv(nil, "sh", "-c", "exit 0"); err != nil {
		t.Errorf("success command should return nil, got %v", err)
	}
}

// TestBusUnreachable: a bus-connection failure is distinguished from a genuine
// enable/restart fault so install can point at the --system fallback.
func TestBusUnreachable(t *testing.T) {
	if !busUnreachable([]string{"systemctl --user daemon-reload: Failed to connect to bus: No such file or directory"}) {
		t.Error("a 'connect to bus' failure should be classed unreachable")
	}
	if busUnreachable([]string{"systemctl --user enable satelle.service: Unit satelle.service is masked"}) {
		t.Error("a plain enable fault should NOT be classed as bus-unreachable")
	}
	if busUnreachable(nil) {
		t.Error("no failures → not unreachable")
	}
}

func TestResolveServeBinary(t *testing.T) {
	exists := func(want string) func(string) bool {
		return func(p string) bool { return p == want }
	}
	// sibling present
	path, fb := resolveServeBinary("/opt/bin/satelle", "", exists("/opt/bin/satelle-serve"))
	if path != "/opt/bin/satelle-serve" || fb {
		t.Fatalf("sibling: path=%q fb=%v", path, fb)
	}
	// flag override
	path, fb = resolveServeBinary("/opt/bin/satelle", "/x/serve", exists("/x/serve"))
	if path != "/x/serve" || fb {
		t.Fatalf("flag: path=%q fb=%v", path, fb)
	}
	// fallback
	path, fb = resolveServeBinary("/opt/bin/satelle", "", func(string) bool { return false })
	if path != "/opt/bin/satelle serve" || !fb {
		t.Fatalf("fallback: path=%q fb=%v", path, fb)
	}
}

// --- serviceStatusLine end-to-end scenarios (sty_acd4b61e AC1/AC5) ---
//
// Every case overrides serviceStatusHooks so NOTHING here ever shells out to the
// real systemctl or touches the operator's actual satelle.service unit — the same
// discipline restartServiceIfRunningRoot's tests already follow (sty_c344d080 AC6).

func withServiceStatusHooks(t *testing.T, h struct {
	userIsActive   func() (string, bool)
	systemIsActive func() (string, bool)
	systemStartLtd func() bool
}) {
	t.Helper()
	prev := serviceStatusHooks
	serviceStatusHooks = h
	t.Cleanup(func() { serviceStatusHooks = prev })
}

func unreachableHooks() struct {
	userIsActive   func() (string, bool)
	systemIsActive func() (string, bool)
	systemStartLtd func() bool
} {
	return struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "", false },
		systemIsActive: func() (string, bool) { return "", false },
		systemStartLtd: func() bool { return false },
	}
}

// writeFakeUserUnit installs a fake ~/.config/systemd/user/satelle.service naming
// execStart as its ExecStart binary, isolating HOME so the real operator unit is
// never read. Returns the unit path.
func writeFakeUserUnit(t *testing.T, execStart string) string {
	t.Helper()
	testutil.IsolateHome(t) // servicePort()'s config.LoadGlobal() needs SATELLE_HOME set
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, serviceUnitName)
	content := "[Service]\nExecStart=" + execStart + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestServiceStatusLine_NotInstalled: (a) no unit file — never derived from a
// systemctl query, purely a filesystem fact.
func TestServiceStatusLine_NotInstalled(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("HOME", t.TempDir()) // empty HOME: no ~/.config/systemd/user/satelle.service
	withServiceStatusHooks(t, unreachableHooks())
	got := serviceStatusLine(t.TempDir())
	if got != "not installed" {
		t.Errorf("serviceStatusLine = %q, want %q", got, "not installed")
	}
}

// TestServiceStatusLine_InstalledNotRunning: (b) unit installed, no process found
// by kernel facts, supervisor reachable and confirms inactive.
func TestServiceStatusLine_InstalledNotRunning(t *testing.T) {
	writeFakeUserUnit(t, "/opt/bin/satelle-serve")
	withServiceStatusHooks(t, struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "inactive", true },
		systemIsActive: func() (string, bool) { return "", false },
		systemStartLtd: func() bool { return false },
	})
	got := serviceStatusLine(t.TempDir()) // empty /proc: no live PID anywhere
	if got != "installed, not running" {
		t.Errorf("serviceStatusLine = %q, want %q", got, "installed, not running")
	}
}

// TestServiceStatusLine_StartLimited: (e) unit installed, not running, and
// systemd reports it start-limited — named explicitly, not folded into "stopped".
func TestServiceStatusLine_StartLimited(t *testing.T) {
	writeFakeUserUnit(t, "/opt/bin/satelle-serve")
	withServiceStatusHooks(t, struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "failed", true },
		systemIsActive: func() (string, bool) { return "", false },
		systemStartLtd: func() bool { return true },
	})
	got := serviceStatusLine(t.TempDir())
	if !strings.Contains(got, "start-limited") {
		t.Errorf("serviceStatusLine = %q, want it to name start-limited", got)
	}
}

// TestServiceStatusLine_RunningReachableActive: (c) process running under the
// unit's cgroup, supervisor reachable and reports active — the WSL-restart-fixed
// case this story was raised over. Also covers AC3 (exe identity matches).
func TestServiceStatusLine_RunningReachableActive(t *testing.T) {
	installDir := t.TempDir()
	target := filepath.Join(installDir, "satelle-serve")
	if err := os.WriteFile(target, []byte("the installed binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeUserUnit(t, target)
	withServiceStatusHooks(t, struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "active", true },
		systemIsActive: func() (string, bool) { return "", false },
		systemStartLtd: func() bool { return false },
	})
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 334, "/user.slice/user-1000.slice/user@1000.service/app.slice/"+serviceUnitName, target)
	got := serviceStatusLine(procRoot)
	for _, want := range []string{"active (user unit, pid 334)", "matches the installed binary"} {
		if !strings.Contains(got, want) {
			t.Errorf("serviceStatusLine = %q, want substring %q", got, want)
		}
	}
}

// TestServiceStatusLine_RunningUnreachable: (d) process running under the unit's
// cgroup, but the supervisor is unreachable — the exact WSL-user-D-Bus-dead
// condition both sty_c344d080 and sty_02acce1b were wrongly parked over. This must
// NEVER render as "not installed" or "stopped" (sty_acd4b61e AC2).
func TestServiceStatusLine_RunningUnreachable(t *testing.T) {
	stale := filepath.Join(t.TempDir(), "old-satelle-serve")
	if err := os.WriteFile(stale, []byte("stale binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "satelle-serve")
	if err := os.WriteFile(current, []byte("current installed binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeUserUnit(t, current)
	withServiceStatusHooks(t, unreachableHooks())
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 334, "/user.slice/user-1000.slice/user@1000.service/app.slice/"+serviceUnitName, stale)
	got := serviceStatusLine(procRoot)
	if !strings.Contains(got, "supervisor is unreachable") {
		t.Errorf("serviceStatusLine = %q, want it to name the supervisor as unreachable", got)
	}
	if !strings.Contains(got, "pid 334") {
		t.Errorf("serviceStatusLine = %q, want the live pid named", got)
	}
	for _, forbidden := range []string{"not installed", "not installed or stopped"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("serviceStatusLine = %q must never collapse a live process with an unreachable supervisor into %q", got, forbidden)
		}
	}
	if !strings.Contains(got, "does NOT match") {
		t.Errorf("serviceStatusLine = %q, want the stale exe identity named", got)
	}
}

// TestServiceStatusLine_EphemeralOutsideSupervision: a process is physically
// found (via the listening port, not the unit's cgroup) while the bus IS
// reachable and reports the unit inactive — an ephemeral relaunch outside the
// installed unit's supervision, the state release.md's dogfood forbids as a
// final state. Must be named distinctly from both "active" and "unreachable".
func TestServiceStatusLine_EphemeralOutsideSupervision(t *testing.T) {
	writeFakeUserUnit(t, "/opt/bin/satelle-serve")
	withServiceStatusHooks(t, struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "inactive", true },
		systemIsActive: func() (string, bool) { return "inactive", true },
		systemStartLtd: func() bool { return false },
	})
	procRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(procRoot, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	port := 8787
	body := "  sl  local_address rem_address   st tx_queue:rx_queue tr:tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:" + strconv.FormatInt(int64(port), 16) + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 88888 1 0 100 0 0 10 0\n"
	if err := os.WriteFile(filepath.Join(procRoot, "net", "tcp"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fdDir := filepath.Join(procRoot, "777", "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[88888]", filepath.Join(fdDir, "3")); err != nil {
		t.Fatal(err)
	}
	got := serviceStatusLine(procRoot)
	if !strings.Contains(got, "ephemeral") {
		t.Errorf("serviceStatusLine = %q, want the ephemeral/outside-supervision state named", got)
	}
	if strings.Contains(got, "active (") {
		t.Errorf("serviceStatusLine = %q must not report active when the unit itself reports inactive", got)
	}
}

// TestServiceStatusLine_IdentityUnknown: AC3's "states plainly when the
// comparison could not be made" branch — the unit file names a binary that does
// not exist on disk, so identityFromPath cannot resolve either side.
func TestServiceStatusLine_IdentityUnknown(t *testing.T) {
	writeFakeUserUnit(t, "/nonexistent/satelle-serve")
	withServiceStatusHooks(t, struct {
		userIsActive   func() (string, bool)
		systemIsActive func() (string, bool)
		systemStartLtd func() bool
	}{
		userIsActive:   func() (string, bool) { return "active", true },
		systemIsActive: func() (string, bool) { return "", false },
		systemStartLtd: func() bool { return false },
	})
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 334, "/user.slice/user-1000.slice/user@1000.service/app.slice/"+serviceUnitName, "/nonexistent/satelle-serve")
	got := serviceStatusLine(procRoot)
	if !strings.Contains(got, "could not be determined") {
		t.Errorf("serviceStatusLine = %q, want the identity-unknown case named plainly", got)
	}
}
