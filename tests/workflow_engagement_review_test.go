//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEngagementBlockedOnMissingExecutorSkill drives the real binary: a story whose
// active workflow's path to done has an executor step with an unresolvable skill
// cannot be engaged — `story set --status in_progress` fails up front and names the
// missing skill (sty_09ef53d6). Deterministic and agent-free: the guard rejects
// before any reviewer runs, so the break is caught at engagement, not after the
// slice is built.
func TestEngagementBlockedOnMissingExecutorSkill(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// The route's path to done runs through an EXECUTOR step whose skill resolves
	// to nothing in the substrate, so engaging a story under it must be rejected up
	// front — before any work — naming the missing skill.
	writeSpineFixture(t, repo, "", "cancelled @satelle-story-cancel-review", "",
		"in_progress|executor|||",
		"ship|executor|bogus-ship-skill||",
		"done|||satelle-story-done-review|reviewer")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("reindex"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	out, err := run("story", "create", "--title", "Engage me",
		"--body", "drive this story to engage the bogus workflow",
		"--acceptance", "1. it engages")
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("parse created id: %v\n%s", err, out)
	}

	got, err := run("story", "set", created.ID, "--status", "in_progress")
	if err == nil {
		t.Fatalf("expected engagement to be rejected, but it succeeded:\n%s", got)
	}
	if !strings.Contains(got, "bogus-ship-skill") {
		t.Errorf("rejection should name the missing executor skill bogus-ship-skill:\n%s", got)
	}

	st, err := run("story", "get", created.ID)
	if err != nil {
		t.Fatalf("story get: %v\n%s", err, st)
	}
	var row struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(st), &row); err != nil {
		t.Fatalf("parse story get: %v\n%s", err, st)
	}
	if row.Status != "backlog" {
		t.Errorf("story status = %q, want backlog (engagement blocked)", row.Status)
	}
}
