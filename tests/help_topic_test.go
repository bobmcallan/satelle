//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestHelpProjectsTopic drives the real binary: the new projects topic is listed
// by `satelle help` and `satelle help projects` teaches the workspace-add path.
func TestHelpProjectsTopic(t *testing.T) {
	dir := t.TempDir()
	if list := mustRun(t, testBin, dir, "help"); !strings.Contains(list, "projects") {
		t.Errorf("`satelle help` does not list the projects topic:\n%s", list)
	}
	body := mustRun(t, testBin, dir, "help", "projects")
	for _, want := range []string{"workspace add", "/<slug>/", "service install"} {
		if !strings.Contains(body, want) {
			t.Errorf("`satelle help projects` missing %q:\n%s", want, body)
		}
	}
}
