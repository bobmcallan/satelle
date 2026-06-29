//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// engagementBogusWorkflow overrides the baseline for all categories. Its path to
// done runs through an EXECUTOR step whose @skill: prompt resolves to nothing in
// the substrate — so engaging a story under it must be rejected up front by the
// deterministic engagement guard, before any work, naming the missing skill.
const engagementBogusWorkflow = `---
name: satelle-baseline-workflow
scope: project
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: Test workflow whose path to done has an executor step with a missing skill.
---

# bogus

` + "```dot" + `
digraph w {
  rankdir=LR
  backlog     [shape=Mdiamond]
  in_progress [actor=executor]
  ship        [actor=executor, prompt="@skill:bogus-ship-skill"]
  done        [shape=Msquare, actor=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled   [actor=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress
  in_progress -> ship
  ship -> done
  backlog -> cancelled
  in_progress -> cancelled
}
` + "```" + `
`

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

	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "satelle-baseline-workflow.md"), []byte(engagementBogusWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command(testBin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("index"); err != nil {
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
