package cli

import (
	"strings"
	"testing"
)

func TestSystemdUnitContent(t *testing.T) {
	unit := systemdUnit("/usr/local/bin/satelle", "/home/u/repo", "0.0.0.0", 8787)
	for _, want := range []string{
		"Description=satelle web server",
		"ExecStart=/usr/local/bin/satelle serve --multi --addr 0.0.0.0 --port 8787",
		"WorkingDirectory=/home/u/repo",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestJoinArgs(t *testing.T) {
	got := joinArgs([]string{"systemctl", "--user", "enable", "--now", "satelle.service"})
	if got != "systemctl --user enable --now satelle.service" {
		t.Errorf("joinArgs = %q", got)
	}
}
