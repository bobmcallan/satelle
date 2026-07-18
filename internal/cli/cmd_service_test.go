package cli

import (
	"strings"
	"testing"
)

func TestSystemdUnitContent(t *testing.T) {
	unit := systemdUnit("/usr/local/bin/satelle", "/home/u/repo", "0.0.0.0", 8787)
	for _, want := range []string{
		"Description=satelle web server",
		"ExecStart=/usr/local/bin/satelle serve --addr 0.0.0.0 --port 8787",
		"WorkingDirectory=", // $HOME preferred; not a single repo (sty_dbdadfa0)
		"Restart=on-failure",
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
	unit := systemSystemdUnit("/usr/local/bin/satelle", "/home/u/repo", "0.0.0.0", 8787, "bobmc")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/satelle serve --addr 0.0.0.0 --port 8787",
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
	// The per-user unit must NOT carry User=/multi-user.target, and keeps on-failure.
	userUnit := systemdUnit("/usr/local/bin/satelle", "/home/u/repo", "0.0.0.0", 8787)
	if strings.Contains(userUnit, "User=") || strings.Contains(userUnit, "multi-user.target") {
		t.Errorf("per-user unit should stay default.target with no User=:\n%s", userUnit)
	}
	if !strings.Contains(userUnit, "Restart=on-failure") {
		t.Errorf("per-user unit should keep Restart=on-failure:\n%s", userUnit)
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
